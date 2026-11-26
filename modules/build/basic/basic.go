// Package basic is the make and shell build backends: the two that run a
// tool of the project's own - a Makefile, a command - rather than one tt
// knows, and the two lifecycle hooks run with.
//
// Either makes modules, data files and programs, as the files of output say
// (build.OutputFile.To). Neither copies them: the core copies them from the
// working directory of the build (build.OutputCwd). A target or a command
// writes what else it makes into TT_OUTPUT_DIR itself.
//
// Either is built without tt too (build.Backend.Exports): the script of its
// export runs the command Run runs, with the variables a build of tt has -
// that of shell by env, which execs it as Run does - so the source rock of a
// package of make and shell builds under LuaRocks as under tt.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package basic

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/tarantool/tt/sdk/build"
)

const (
	// MakeName is the name of the make backend.
	MakeName = "make"
	// ShellName is the name of the shell backend.
	ShellName = "shell"
)

// makeTool is the program the make backend runs.
const makeTool = "make"

// defaultMakefile is the entrypoint of a make build that names none.
const defaultMakefile = "Makefile"

// Backends returns the make and shell backends.
func Backends() []build.Backend {
	return []build.Backend{Make(), Shell()}
}

// Make returns the make backend: make -C <cwd> -f <entrypoint> <make_target>
// <flags...>.
func Make() build.Backend {
	return build.Backend{
		Name:           MakeName,
		Produces:       build.Results(),
		Config:         func() any { return new(MakeConfig) },
		New:            build.Typed(newMake),
		Incremental:    true,
		OutputFrom:     build.OutputCwd,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           true,
		// The Makefile is told the platform in TT_PLATFORM_OS and
		// TT_PLATFORM_ARCH, and the core checks what it made against it.
		Cross: true,
		// The script of a build runs the make Run runs.
		Exports: true,
	}
}

// Shell returns the shell backend: <command> <args...>, run as it is, with
// no shell parsing - a pipeline needs a script of its own.
func Shell() build.Backend {
	return build.Backend{
		Name:           ShellName,
		Produces:       build.Results(),
		Config:         func() any { return new(ShellConfig) },
		New:            build.Typed(newShell),
		Incremental:    true,
		OutputFrom:     build.OutputCwd,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           true,
		// The command is told the platform in TT_PLATFORM_OS and
		// TT_PLATFORM_ARCH, and the core checks what it made against it.
		Cross: true,
		// The script of a build runs the command Run runs.
		Exports: true,
	}
}

// MakeConfig are the fields of a make build block.
type MakeConfig struct {
	// MakeTarget is the target make builds. Required.
	MakeTarget string `toml:"make_target"`
	// Entrypoint is the makefile, relative to the working directory;
	// Makefile by default.
	Entrypoint string `toml:"entrypoint"`
	// Flags are given to make after the target: -j4.
	Flags []string `toml:"flags"`
}

// ShellConfig are the fields of a shell build block.
type ShellConfig struct {
	// Command is the program run. Required.
	Command string `toml:"command"`
	// Args are its arguments, one per entry.
	Args []string `toml:"args"`
}

// newMake checks a make build block.
func newMake(config *MakeConfig, _ build.Component, common build.Common) (build.Build, error) {
	if config.MakeTarget == "" {
		return nil, build.FieldErrorf("", "make_target is required for backend %q", MakeName)
	}

	return makeBuild{config: *config, outputs: common.Output}, nil
}

// newShell checks a shell build block.
func newShell(config *ShellConfig, _ build.Component, common build.Common) (build.Build, error) {
	if config.Command == "" {
		return nil, build.FieldErrorf("", "command is required for backend %q", ShellName)
	}

	return shellBuild{config: *config, outputs: common.Output}, nil
}

// outputs are the files of the output of a make or shell build block: what
// the build makes at least, besides what it writes into the directories of
// the Context itself.
type outputs []build.OutputFile

// Produces names the types of the files of output.
func (o outputs) Produces() []build.Result {
	var results []build.Result

	for _, file := range o {
		if !slices.Contains(results, file.To) {
			results = append(results, file.To)
		}
	}

	return results
}

// Programs names the programs of output, by base name, which is what the
// core copies them as. A target or a command may write others into the
// directory of the programs.
func (o outputs) Programs() ([]string, bool) {
	var programs []string

	for _, file := range o {
		if file.To == build.Program {
			programs = append(programs, file.Path)
		}
	}

	return outputNames(programs), false
}

// makeBuild is a checked make build block.
type makeBuild struct {
	outputs

	config MakeConfig
}

// Run runs make in the working directory of the build.
func (b makeBuild) Run(ctx context.Context, c *build.Context) error {
	return c.Run(c.Command(ctx, makeTool, makeArgs(b.config, c.Dirs.Cwd)...))
}

// Export runs make as Run does, in the working directory of the build the
// script runs in: make -C . -f <entrypoint> <make_target> <flags...>.
func (b makeBuild) Export(*build.ExportContext) (build.Export, error) {
	script, err := renderScript("command", commandView{
		Name: makeTool, Args: makeArgs(b.config, "."),
	})
	if err != nil {
		return build.Export{}, err
	}

	return build.Export{Script: script, Tools: []string{makeTool}, Lost: nil}, nil
}

// makeArgs is the argv of make in cwd: -C <cwd> -f <entrypoint>
// <make_target> <flags...>.
func makeArgs(config MakeConfig, cwd string) []string {
	entrypoint := config.Entrypoint
	if entrypoint == "" {
		entrypoint = defaultMakefile
	}

	return append([]string{"-C", cwd, "-f", entrypoint, config.MakeTarget}, config.Flags...)
}

// shellBuild is a checked shell build block.
type shellBuild struct {
	outputs

	config ShellConfig
}

// Run runs the command in the working directory of the build.
func (b shellBuild) Run(ctx context.Context, c *build.Context) error {
	return c.Run(c.Command(ctx, b.config.Command, b.config.Args...))
}

// Export runs the command as Run does, each argument the word it is in the
// block: by env -- <command> <args...>, which execs it from PATH as Run
// does, so that a command named as a word sh reads itself - a reserved word,
// if, a builtin, test or ., a function of the script - runs the program of
// the name all the same. A command with a = is made with tt alone
// (build.ErrNoExport): env takes it for a variable to set. A command of a
// bare name is looked for in PATH before the build, as a command with a
// slash, a path from the working directory, is not.
func (b shellBuild) Export(*build.ExportContext) (build.Export, error) {
	if strings.Contains(b.config.Command, "=") {
		return build.Export{}, build.NoExportf("command", "%q has a =, and the script runs "+
			"the command by env, as Run execs it, which takes a word with a = for a variable "+
			"to set", b.config.Command)
	}

	var tools []string

	if !strings.Contains(b.config.Command, "/") {
		tools = []string{b.config.Command}
	}

	script, err := renderScript("env", commandView{
		Name: b.config.Command, Args: b.config.Args,
	})
	if err != nil {
		return build.Export{}, err
	}

	return build.Export{Script: script, Tools: tools, Lost: nil}, nil
}

// commandView is a command of sh of the templates of export.sh.tmpl: Name
// with Args, each a word of its own (build.ShellQuote).
type commandView struct {
	Name string
	Args []string
}

// exportText is export.sh.tmpl, the templates of the scripts of the exports
// of make and shell.
//
//go:embed export.sh.tmpl
var exportText string

// exportFuncs are the functions of the templates: q is a string as one word
// of sh (build.ShellQuote).
var exportFuncs = template.FuncMap{"q": build.ShellQuote}

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

// outputNames returns the base names of the files of output, each once, in
// their order: what the core copies them into their directory as.
func outputNames(output []string) []string {
	names := make([]string, 0, len(output))

	for _, entry := range output {
		name := filepath.Base(entry)
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names
}
