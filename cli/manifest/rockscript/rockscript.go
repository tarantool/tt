// Package rockscript renders tt-rock.sh, the script of POSIX sh that builds a
// tt package without tt: the rockspec of the rock of the package runs it, by
// build type command, so that LuaRocks - tt rocks of tt 2, a LuaRocks
// configured for Tarantool - builds the source rock of a package whose
// components or hooks have build blocks.
//
// The script is what tt does when it builds the package, but for what only
// tt does: it sets the variables of a build of tt, from what LuaRocks gives
// it and the machine it runs on; runs the hook pre_build, the builds of the
// components in the order of the product, each in a subshell in its working
// directory, the script of the export of its backend (build.Exporter), and
// copies the files the output of a backend of build.OutputCwd lists after
// it; lays out the files of the layout of tt that build.install of the
// rockspec does not install; runs the hook post_build. Everything goes into
// a stage, .tt-rock/stage/{lua,lib,bin}, which the install phase copies into
// the directories of the rock. The knowledge of how to build a component
// without tt is the backend's; the package knows the frame alone.
package rockscript

import (
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	sdkbuild "github.com/tarantool/tt/sdk/build"
)

// The directories of the stage, as the rock of a package has them.
const (
	stageLua = "lua"
	stageLib = "lib"
	stageBin = "bin"
)

// Script is the build of a package without tt that Render writes.
type Script struct {
	// Package and Version are the name and the version of the package,
	// TT_PACKAGE and TT_VERSION.
	Package, Version string
	// PreBuild and PostBuild are the hooks pre_build and post_build of the
	// package, nil when it has none.
	PreBuild, PostBuild *Step
	// Components are the builds of the components of the product, in its
	// order.
	Components []Step
	// Lay are the files of the project the layout of tt takes into the stage
	// that build.install of the rockspec does not install: the script copies
	// each after the builds of the components, as tt lays them out, when it
	// is there - a file of the layout a build made in the directory of its
	// component, say.
	Lay []Lay
}

// Step is a build of the script: of a component, or of a hook.
type Step struct {
	// Component is the name of the component built; empty for a hook.
	Component string
	// Hook is the name of the hook, pre_build or post_build; empty for a
	// component.
	Hook string
	// Backend is the name of the backend that builds it.
	Backend string
	// Cwd is the working directory of the build, slash-separated and
	// relative to the root of the project, which it is in; empty or "." for
	// the root.
	Cwd string
	// Env is the env of the build block (CheckEnv). The variables the
	// script sets for the build win over it, as those of tt do.
	Env map[string]string
	// Namespace is the namespace of the component, the directory of its
	// modules and data files under lib/ and lua/ of the stage; empty for a
	// flat layout.
	Namespace string
	// ProgramsOnly makes TT_OUTPUT_DIR the directory of the programs, as it
	// is for a component of kind = "executable".
	ProgramsOnly bool
	// Export is the export of the build.
	Export sdkbuild.Export
	// Outputs are the files of output the script copies after the script of
	// the export, as the core copies those of a backend of
	// sdkbuild.OutputCwd; none for another backend, which copies them
	// itself.
	Outputs []sdkbuild.OutputFile
}

// What names the build in the comments and the messages of the script: the
// component "native", the hook pre_build.
func (s Step) What() string {
	if s.Component == "" {
		return "the hook " + s.Hook
	}

	return fmt.Sprintf("the component %q", s.Component)
}

// Lay is a file of the project the script lays out into the stage
// (Script.Lay).
type Lay struct {
	// Source is the path of the file in the project, slash-separated.
	Source string
	// Dest is its path in the stage, slash-separated: under lua/ or lib/.
	Dest string
}

// nameRe is the shape of the name of a variable of POSIX sh.
var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	// ErrEnv reports a variable of the env of a build block the script
	// cannot set (CheckEnv).
	ErrEnv = errors.New("the variable has no name of sh")
	// ErrNUL reports a value of a build the script would hold with a NUL
	// byte in it (CheckEnv, CheckStep): no string of sh holds one.
	ErrNUL = errors.New("holds a NUL byte, which no script of sh holds")
)

// CheckEnv reports a variable of env, the env of a build block, that the
// script cannot set: one whose name is no name of a variable of POSIX sh,
// wrapping ErrEnv, or whose value holds a NUL byte, wrapping ErrNUL.
func CheckEnv(env map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(env)) {
		if !nameRe.MatchString(key) {
			return fmt.Errorf("%w: %q", ErrEnv, key)
		}

		if strings.Contains(env[key], "\x00") {
			return fmt.Errorf("the value of %s %w", key, ErrNUL)
		}
	}

	return nil
}

// CheckStep reports, wrapping ErrNUL, a value of step but its env
// (CheckEnv) that the script would hold with a NUL byte in it: the script of
// its export, a tool or a sentence of what it lacks, its working directory,
// or the path of a file of its output.
func CheckStep(step Step) error {
	outputs := make([]string, 0, len(step.Outputs))
	for _, file := range step.Outputs {
		outputs = append(outputs, file.Path)
	}

	for _, value := range []struct {
		what  string
		texts []string
	}{
		{"the script of its export", []string{step.Export.Script}},
		{"a tool of its export", step.Export.Tools},
		{"what its export lacks", step.Export.Lost},
		{"its working directory", []string{step.Cwd}},
		{"a file of its output", outputs},
	} {
		if slices.ContainsFunc(value.texts, func(text string) bool {
			return strings.Contains(text, "\x00")
		}) {
			return fmt.Errorf("%s %w", value.what, ErrNUL)
		}
	}

	return nil
}

// globalVariables are the variables the script sets for every build, which
// the env of a block does not change.
//
//nolint:gochecknoglobals // A constant list.
var globalVariables = []string{
	"TT_PROJECT_ROOT", "TT_PACKAGE", "TT_VERSION", "TT_PLATFORM_OS", "TT_PLATFORM_ARCH",
	"TT_BUILD_JOBS", "TT_STATE_DIR", "TT_LINK_ROOT", "TT_MODULE_EXT", "TT_MODULE_LDFLAGS",
	"TT_TARANTOOL_INCLUDE_DIR", "TT_TARANTOOL_PREFIX",
}

// componentVariables are the variables the script sets for the build of a
// component besides globalVariables.
//
//nolint:gochecknoglobals // A constant list.
var componentVariables = []string{
	"TT_COMPONENT_NAME", "TT_OUTPUT_DIR", "TT_SHARE_DIR", "TT_PROGRAMS_DIR", "TT_BUILD_DIR",
	"TT_LINK_DIR",
}

// Render writes the script, which runs as sh ./tt-rock.sh build and sh
// ./tt-rock.sh install from the root of the sources (see the package). The
// text of the script is the template tt-rock.sh.tmpl, which Render executes
// on the view of script.
func Render(script Script) string {
	var out strings.Builder

	err := scriptTemplate.Execute(&out, newView(script))
	if err != nil {
		// The template is the package's and the view is typed, and a
		// strings.Builder takes any write: an error is a bug of the
		// package.
		panic("rockscript: the template of the script fails: " + err.Error())
	}

	return out.String()
}

// scriptText is the template of the script (Render).
//
//go:embed tt-rock.sh.tmpl
var scriptText string

// scriptTemplate is scriptText, parsed. Its functions are q, a word of sh
// (sdkbuild.ShellQuote), comment and capitalize.
//
//nolint:gochecknoglobals // A constant template.
var scriptTemplate = template.Must(template.New("tt-rock.sh").Option("missingkey=error").
	Funcs(template.FuncMap{
		"q":          sdkbuild.ShellQuote,
		"comment":    comment,
		"capitalize": capitalize,
	}).Parse(scriptText))

// steps are the builds of the script in the order it runs them.
func (s Script) steps() []Step {
	var steps []Step

	if s.PreBuild != nil {
		steps = append(steps, *s.PreBuild)
	}

	steps = append(steps, s.Components...)

	if s.PostBuild != nil {
		steps = append(steps, *s.PostBuild)
	}

	return steps
}

// view is a Script as the template of the script takes it: its steps in
// the order the script runs them, and the words of sh of its values that
// are more than a value quoted, which the template quotes with q.
type view struct {
	// Package and Version are those of the script.
	Package, Version string
	// Steps are the builds of the script in the order it runs them
	// (Script.steps).
	Steps []stepView
	// LayAt is the number of the steps the script runs before it lays out
	// Lay: the layout comes after the builds of the components and before
	// post_build, as in tt.
	LayAt int
	// Lay are the files the script lays out into the stage (Script.Lay).
	Lay []Lay
}

// stepView is a Step as the template of the script takes it.
type stepView struct {
	// Number is the number of the step in the order the script runs it,
	// from 1, which names its functions tt_step_<n> and tt_export_<n>.
	Number int
	// What names the step (Step.What), Backend is its backend.
	What, Backend string
	// Component is the name of the component built, empty for a hook,
	// which has none of the variables of a component.
	Component string
	// Env are the variables of the env of the block the script exports for
	// the step, by name: all but those it sets itself (globalVariables,
	// componentVariables).
	Env []envVar
	// OutputDir and ShareDir are TT_OUTPUT_DIR and TT_SHARE_DIR of a
	// component, as values of an assignment of sh.
	OutputDir, ShareDir string
	// Cwd is the working directory of the step, as a word of sh.
	Cwd string
	// Outputs are the files of output the script copies after the script
	// of the export.
	Outputs []outputView
	// Script is the script of the export as it is, ending with a newline
	// (exportScript).
	Script string
	// Tools and Lost are those of the export.
	Tools, Lost []string
	// HasPath tells that the env of the block has PATH, Path, in which the
	// tools of the export are looked up.
	HasPath bool
	Path    string
}

// envVar is a variable of the env of a block, Name=Value.
type envVar struct {
	Name, Value string
}

// outputView is a file of output the script copies: Path as the output
// names it, to Dest, a word of sh (outputDest).
type outputView struct {
	Path, Dest string
}

// newView is the view of script the template executes on.
func newView(script Script) view {
	steps := script.steps()

	// The layout comes after the builds of the components and before
	// post_build, as in tt.
	layAt := slices.IndexFunc(steps, func(step Step) bool {
		return step.Component == "" && step.Hook != hookPreBuild
	})
	if layAt < 0 {
		layAt = len(steps)
	}

	views := make([]stepView, 0, len(steps))

	for index, step := range steps {
		views = append(views, newStepView(index+1, step))
	}

	return view{
		Package: script.Package, Version: script.Version, Steps: views, LayAt: layAt,
		Lay: script.Lay,
	}
}

// newStepView is the view of step, the step numbered number.
func newStepView(number int, step Step) stepView {
	taken := slices.Clone(globalVariables)
	if step.Component != "" {
		taken = append(taken, componentVariables...)
	}

	var env []envVar

	for _, key := range slices.Sorted(maps.Keys(step.Env)) {
		if !slices.Contains(taken, key) {
			env = append(env, envVar{Name: key, Value: step.Env[key]})
		}
	}

	outputs := make([]outputView, 0, len(step.Outputs))

	for _, file := range step.Outputs {
		outputs = append(outputs, outputView{Path: file.Path, Dest: outputDest(step, file)})
	}

	output := stageDir(stageLib, step.Namespace)
	if step.ProgramsOnly {
		output = stageDir(stageBin, "")
	}

	pathValue, hasPath := step.Env["PATH"]

	return stepView{
		Number: number, What: step.What(), Backend: step.Backend, Component: step.Component,
		Env: env, OutputDir: output, ShareDir: stageDir(stageLua, step.Namespace),
		Cwd: projectPath(step.Cwd), Outputs: outputs, Script: exportScript(step),
		Tools: step.Export.Tools, Lost: step.Export.Lost, HasPath: hasPath, Path: pathValue,
	}
}

// exportScript is the script of the export of step as tt_export_<n> holds
// it: as it is, but ":" for one of blanks alone, and ending with a newline.
func exportScript(step Step) string {
	script := step.Export.Script
	if strings.TrimSpace(script) == "" {
		script = ":"
	}

	if !strings.HasSuffix(script, "\n") {
		script += "\n"
	}

	return script
}

// stageDir is the directory dir of the stage, lib, lua or bin, under
// namespace when it has one, as the value of an assignment of sh.
func stageDir(dir, namespace string) string {
	if namespace == "" {
		return "$tt_stage/" + dir
	}

	return "$tt_stage/" + dir + "/" + sdkbuild.ShellQuote(namespace)
}

// projectPath is the directory rel of the project, relative to its root and
// slash-separated, as a word of sh.
func projectPath(rel string) string {
	rel = path.Clean("/" + rel)[1:]
	if rel == "" {
		return `"$tt_root"`
	}

	return `"$tt_root"/` + sdkbuild.ShellQuote(rel)
}

// linkMark stands for TT_LINK_DIR in the paths outputDest has
// sdkbuild.Dirs.OutputPath make.
const linkMark = "@link"

// outputDest is where the build of step copies file of its output, as a word
// of sh: the directory of its type in the stage, or TT_LINK_DIR, as
// sdkbuild.Dirs.OutputPath has it.
func outputDest(step Step, file sdkbuild.OutputFile) string {
	dirs := sdkbuild.Dirs{
		Project: "", Component: "", Cwd: "", Work: "", State: "", Tools: "", Tree: "",
		Modules:  path.Join(stageLib, step.Namespace),
		Share:    path.Join(stageLua, step.Namespace),
		Programs: stageBin,
		Link:     linkMark,
	}

	dest := filepath.ToSlash(dirs.OutputPath(file.To, path.Base(file.Path)))

	if rel, ok := strings.CutPrefix(dest, linkMark+"/"); ok {
		return `"$TT_LINK_DIR"/` + sdkbuild.ShellQuote(rel)
	}

	return `"$tt_stage"/` + sdkbuild.ShellQuote(dest)
}

// hookPreBuild is the hook the script runs before the builds of the
// components.
const hookPreBuild = "pre_build"

// comment is text on one line of a comment of the script.
func comment(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// capitalize is text with its first letter in upper case.
func capitalize(text string) string {
	if text == "" {
		return text
	}

	return strings.ToUpper(text[:1]) + text[1:]
}
