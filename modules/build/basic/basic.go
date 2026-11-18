// Package basic is the make and shell build backends: the two that run a
// tool of the project's own - a Makefile, a command - rather than one tt
// knows, and the two lifecycle hooks run with.
//
// Either makes modules, data files and programs, as the files of output say
// (build.OutputFile.To). Neither copies them: the core copies them from the
// working directory of the build (build.OutputCwd). A target or a command
// writes what else it makes into TT_OUTPUT_DIR itself.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package basic

import (
	"context"
	"path/filepath"
	"slices"

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
