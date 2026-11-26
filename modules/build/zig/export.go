package zig

import (
	_ "embed"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/tarantool/tt/sdk/build"
)

// This file is the build of zig without tt (build.Exporter): a script of sh
// that runs the zig build Run runs and copies what it installed as Run
// does, the template script of export.sh.tmpl.

// exportText is export.sh.tmpl, the templates of the script of an export.
//
//go:embed export.sh.tmpl
var exportText string

// exportFuncs are the functions of the templates: q is a string as one word
// of sh (build.ShellQuote), join strings.Join.
var exportFuncs = template.FuncMap{"q": build.ShellQuote, "join": strings.Join}

// exportTemplate is exportText parsed once, a key the data has not failing
// it.
var exportTemplate = template.Must(template.New("export.sh.tmpl").Funcs(exportFuncs).
	Option("missingkey=error").Parse(exportText))

// renderScript is the template name of export.sh.tmpl executed on data, the
// view the template is written for: it fails on a mistake of the template
// alone.
func renderScript(name string, data any) (string, error) {
	var out strings.Builder

	err := exportTemplate.ExecuteTemplate(&out, name, data)
	if err != nil {
		return "", fmt.Errorf("render the script of the export: %w", err)
	}

	return out.String(), nil
}

// Export runs zig build as Run does, for the machine the script runs on:
// the steps, the prefix, the cache and the packages in TT_BUILD_DIR, as
// dirArgs has them of the zig of the script, the mode and the options of
// args, with the jobs of a build of no -j, zig's own default; a static
// program alone for the musl target of the machine, when the build.zig
// takes -Dtarget, as targetArg builds it, and none for macOS, as Preflight
// refuses it. It copies the module from lib/ of the prefix, under the
// first of the names of moduleCandidates there, and the program from bin/,
// as copyModule and copyProgram do. The script loses nothing of Run: zig
// fetches the dependencies of build.zig.zon as it does under tt.
func (b zigBuild) Export(*build.ExportContext) (build.Export, error) {
	options := make([]optionView, 0, len(b.config.Options))

	for _, name := range sortedKeys(b.config.Options) {
		options = append(options, optionView{Name: name, Value: b.config.Options[name]})
	}

	targets := make([]targetView, 0, len(zigArches))

	for _, goarch := range slices.Sorted(maps.Keys(zigArches)) {
		targets = append(targets, targetView{
			Arch: goarch, Target: zigTarget(build.Target{OS: "linux", Arch: goarch}, true),
		})
	}

	candidates := moduleCandidates(b.module)
	names := make([]string, 0, len(candidates))

	for _, name := range candidates {
		names = append(names, filepath.ToSlash(name))
	}

	modulePath := path.Join(strings.Split(b.module, ".")...)

	moduleDir := path.Dir(modulePath)
	if moduleDir == "." {
		moduleDir = ""
	}

	script, err := renderScript("script", scriptView{
		Static: b.static && b.bin != "", ErrStaticDarwin: ErrStaticDarwin.Error(),
		Steps: b.config.Steps, ReleaseMode: releaseModes[b.optimize()], Options: options,
		Musl: b.exportsMusl(), Targets: targets,
		Module: b.module, Candidates: candidates, Names: names, ModulePath: modulePath,
		ModuleDir: moduleDir, Bin: b.bin, ErrNoArtifact: errNoArtifact.Error(),
	})
	if err != nil {
		return build.Export{}, err
	}

	return build.Export{Script: script, Tools: []string{zigTool}, Lost: nil}, nil
}

// scriptView is what the template script of export.sh.tmpl renders.
type scriptView struct {
	// Static tells whether the build is of a static program, which the
	// script refuses for macOS with ErrStaticDarwin, as Preflight does.
	Static          bool
	ErrStaticDarwin string
	// Steps are the steps of the block, ReleaseMode the --release of the
	// mode of the build, empty for none, and Options the options of the
	// block, by name.
	Steps       []string
	ReleaseMode string
	Options     []optionView
	// Musl tells whether the script asks for the musl target of the
	// machine (exportsMusl), of Targets.
	Musl    bool
	Targets []targetView
	// Module is the module the script copies, empty for none: the first of
	// Candidates, Names being them as paths of sh, in lib/ of the prefix, to
	// ModulePath of TT_OUTPUT_DIR, in its directory ModuleDir, empty for
	// none.
	Module                string
	Candidates, Names     []string
	ModulePath, ModuleDir string
	// Bin is the program the script copies, empty for none.
	Bin string
	// ErrNoArtifact is the error the script fails with on a module or a
	// program zig build did not install, as Run does.
	ErrNoArtifact string
}

// optionView is an option of the block, -D<Name>=<Value>.
type optionView struct {
	Name, Value string
}

// targetView is an architecture of Go, Arch, and the musl target of zig of
// Linux of it, Target (zigTarget).
type targetView struct {
	Arch, Target string
}

// exportsMusl reports whether the script asks for the musl target of the
// machine, as wantsMusl does but of the platform, which the script tells: a
// static build of a program alone, whose options name no target of their
// own.
func (b zigBuild) exportsMusl() bool {
	_, own := b.config.Options["target"]

	return b.static && b.bin != "" && b.module == "" && !own
}
