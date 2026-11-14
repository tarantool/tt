// Package basic is the make and shell build backends: the two that run a
// tool of the project's own - a Makefile, a command - rather than one tt
// knows, and the two lifecycle hooks run with.
//
// Neither copies the files output lists: the core copies them from the
// working directory of the build (build.OutputCwd). A target or a command
// writes what else it makes into TT_OUTPUT_DIR itself.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package basic

import (
	"context"
	"fmt"
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
		Name:        MakeName,
		Kinds:       []string{build.KindModule, build.KindExecutable},
		Config:      func() any { return new(MakeConfig) },
		New:         newMake,
		Incremental: true,
		OutputFrom:  build.OutputCwd,
		Exclude:     nil,
		Hook:        true,
	}
}

// Shell returns the shell backend: <command> <args...>, run as it is, with
// no shell parsing - a pipeline needs a script of its own.
func Shell() build.Backend {
	return build.Backend{
		Name:        ShellName,
		Kinds:       []string{build.KindModule, build.KindExecutable},
		Config:      func() any { return new(ShellConfig) },
		New:         newShell,
		Incremental: true,
		OutputFrom:  build.OutputCwd,
		Exclude:     nil,
		Hook:        true,
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
func newMake(cfg any, _ build.Component, common build.Common) (build.Build, error) {
	config, ok := cfg.(*MakeConfig)
	if !ok {
		return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
	}

	if config.MakeTarget == "" {
		return nil, build.FieldErrorf("", "make_target is required for backend %q", MakeName)
	}

	return makeBuild{config: *config, output: common.Output}, nil
}

// newShell checks a shell build block.
func newShell(cfg any, _ build.Component, common build.Common) (build.Build, error) {
	config, ok := cfg.(*ShellConfig)
	if !ok {
		return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
	}

	if config.Command == "" {
		return nil, build.FieldErrorf("", "command is required for backend %q", ShellName)
	}

	return shellBuild{config: *config, output: common.Output}, nil
}

// makeBuild is a checked make build block.
type makeBuild struct {
	config MakeConfig
	output []string
}

// Run runs make in the working directory of the build.
func (b makeBuild) Run(ctx context.Context, c *build.Context) error {
	return c.Run(c.Command(ctx, makeTool, makeArgs(b.config, c.Dirs.Cwd)...))
}

// Programs names the programs of an executable component: the files output
// lists, by base name. A target may write others into TT_OUTPUT_DIR.
func (b makeBuild) Programs() ([]string, bool) {
	return outputNames(b.output), false
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
	config ShellConfig
	output []string
}

// Run runs the command in the working directory of the build.
func (b shellBuild) Run(ctx context.Context, c *build.Context) error {
	return c.Run(c.Command(ctx, b.config.Command, b.config.Args...))
}

// Programs names the programs of an executable component: the files output
// lists, by base name. The command may write others into TT_OUTPUT_DIR.
func (b shellBuild) Programs() ([]string, bool) {
	return outputNames(b.output), false
}

// outputNames returns the base names of the files output lists, each once,
// in their order: what the core copies them into the output directory as.
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
