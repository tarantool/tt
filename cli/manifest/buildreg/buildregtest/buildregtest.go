// Package buildregtest is a registry of build backends for the tests of the
// core. The core has no backend of its own: the backends of tt are those of
// its modules (modules/build), and no package of the core imports a module.
//
// Make and Shell are doubles of the make and shell backends of modules/build:
// their names, their fields and their builds. Hooks run with them, shell when
// a hook names no backend, and most components of the tests are built with
// them.
//
// The others are fakes, one of each sort of backend tt has, with fields of
// their own; each builds what its block names, of a program of the host, and
// nothing a real tool would:
//   - exe makes programs only, as go does, and lays out no sources of its
//     component;
//   - lib makes modules only, as c and lua-c do, and needs the headers of
//     Tarantool;
//   - both makes a module, a program or both, as cargo does, and leaves its
//     tool's target/ out of the layout;
//   - tree copies the files output lists itself, as cmake does.
//
// make, shell and both build for another platform (build.Backend.Cross), as
// make, shell and cargo do; the others do not.
package buildregtest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
)

// Module is the name of the module the backends are registered as.
const Module = "buildregtest"

const (
	// MakeName is the name of the double of make.
	MakeName = "make"
	// ShellName is the name of the double of shell.
	ShellName = "shell"
	// ExeName is the name of the fake of a backend of programs.
	ExeName = "exe"
	// LibName is the name of the fake of a backend of modules.
	LibName = "lib"
	// BothName is the name of the fake of a backend of modules and programs.
	BothName = "both"
	// TreeName is the name of the fake of a backend that copies its output.
	TreeName = "tree"
)

// ErrNoHeaders reports a build of lib for a Tarantool without headers.
var ErrNoHeaders = errors.New("lib needs the headers of Tarantool")

// Registry returns a registry of Backends, registered by the module Module.
func Registry() *buildreg.Registry {
	registry := buildreg.New()

	err := registry.Add(Module, Backends(), nil)
	if err != nil {
		// The backends are this package's to keep well formed.
		panic(fmt.Sprintf("buildregtest: %v", err))
	}

	return registry
}

// Backends returns the backends of the tests in the order a module of tt
// gives them: make and shell, then the others by name.
func Backends() []build.Backend {
	return []build.Backend{Make(), Shell(), Both(), Exe(), Lib(), Tree()}
}

// MakeConfig are the fields of a block of make.
type MakeConfig struct {
	// MakeTarget is the target make builds. Required.
	MakeTarget string `toml:"make_target"`
	// Entrypoint is the makefile, Makefile by default.
	Entrypoint string `toml:"entrypoint"`
	// Flags are given to make after the target.
	Flags []string `toml:"flags"`
}

// ShellConfig are the fields of a block of shell.
type ShellConfig struct {
	// Command is the program run. Required.
	Command string `toml:"command"`
	// Args are its arguments.
	Args []string `toml:"args"`
}

// Make returns the double of make: make -C <cwd> -f <entrypoint>
// <make_target> <flags...>.
func Make() build.Backend {
	return build.Backend{
		Name:     MakeName,
		Produces: build.Results(),
		Config:   func() any { return new(MakeConfig) },
		New: func(cfg any, _ build.Component, common build.Common) (build.Build, error) {
			config, ok := cfg.(*MakeConfig)
			if !ok {
				return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
			}

			if config.MakeTarget == "" {
				return nil, build.FieldErrorf("", "make_target is required for backend %q",
					MakeName)
			}

			entrypoint := config.Entrypoint
			if entrypoint == "" {
				entrypoint = "Makefile"
			}

			return commandBuild{
				outputs: common.Output, name: "make",
				args: append([]string{"-f", entrypoint, config.MakeTarget}, config.Flags...),
			}, nil
		},
		Incremental:    true,
		OutputFrom:     build.OutputCwd,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           true,
		Cross:          true,
	}
}

// Shell returns the double of shell: <command> <args...>.
func Shell() build.Backend {
	return build.Backend{
		Name:     ShellName,
		Produces: build.Results(),
		Config:   func() any { return new(ShellConfig) },
		New: func(cfg any, _ build.Component, common build.Common) (build.Build, error) {
			config, ok := cfg.(*ShellConfig)
			if !ok {
				return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
			}

			if config.Command == "" {
				return nil, build.FieldErrorf("", "command is required for backend %q",
					ShellName)
			}

			return commandBuild{outputs: common.Output, name: config.Command, args: config.Args},
				nil
		},
		Incremental:    true,
		OutputFrom:     build.OutputCwd,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           true,
		Cross:          true,
	}
}

// outputs are the files of the output of a block: what the build makes at
// least, besides what it writes into the directories of its Context itself.
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

// programs returns the base names of the programs of output, each once.
func (o outputs) programs() []string {
	var names []string

	for _, file := range o {
		name := filepath.Base(file.Path)
		if file.To == build.Program && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names
}

// commandBuild is a block of make or shell: a command run in the working
// directory of the build.
type commandBuild struct {
	outputs

	name string
	args []string
}

// Programs names the programs of output: a command may make others.
func (b commandBuild) Programs() ([]string, bool) {
	names := b.programs()
	if names == nil {
		names = []string{}
	}

	return names, false
}

// Run runs the command in the working directory of the build.
func (b commandBuild) Run(ctx context.Context, c *build.Context) error {
	return c.Run(c.Command(ctx, b.name, b.args...))
}

// ExeConfig are the fields of a block of exe.
type ExeConfig struct {
	// Name is the name of the program, the component's by default.
	Name string `toml:"name"`
	// Tool is the program the build needs in its PATH, sh by default.
	Tool string `toml:"tool"`
	// Tags and Ldflags are fields of a tool of programs; Ldflags has no
	// empty entry.
	Tags    []string `toml:"tags"`
	Ldflags []string `toml:"ldflags"`
	// Cgo is a field of a tool of programs.
	Cgo bool `toml:"cgo"`
}

// Exe returns the fake of a backend of programs.
func Exe() build.Backend {
	return build.Backend{
		Name:     ExeName,
		Produces: []build.Result{build.Program},
		Config:   func() any { return new(ExeConfig) },
		New: func(cfg any, component build.Component, _ build.Common) (build.Build, error) {
			config, ok := cfg.(*ExeConfig)
			if !ok {
				return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
			}

			err := fileName("name", config.Name)
			if err == nil {
				err = noEmpty("ldflags", config.Ldflags)
			}

			if err != nil {
				return nil, err
			}

			name := config.Name
			if name == "" {
				name = component.Name
			}

			return exeBuild{name: name, tool: orDefault(config.Tool, "sh")}, nil
		},
		Incremental:    false,
		OutputFrom:     build.OutputNone,
		Exclude:        nil,
		NoSourceLayout: true,
		Hook:           false,
		Cross:          false,
	}
}

// exeBuild is a block of exe.
type exeBuild struct {
	name, tool string
}

// Programs names the program of the block.
func (b exeBuild) Programs() ([]string, bool) { return []string{b.name}, true }

// Preflight finds the tool of the block.
func (b exeBuild) Preflight(_ context.Context, c *build.Context) error {
	return needTool(c, ExeName, b.tool)
}

// Run makes the program, a copy of a program of the host.
func (b exeBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err == nil {
		err = copyHostProgram(filepath.Join(c.Dirs.Programs, b.name))
	}

	return err
}

// LibConfig are the fields of a block of lib.
type LibConfig struct {
	LibFlags

	// Module is the name of the module, which the build makes as
	// <module>.so. Required.
	Module string `toml:"module"`
	// Sources and Defines are fields of a compiler.
	Sources []string `toml:"sources"`
	Defines []string `toml:"defines"`
	// Platforms are flags by OS, added to those of the block.
	Platforms map[string]LibFlags `toml:"platforms"`
	// Link are the components whose libraries the build links, as it names
	// them (build.Linker).
	Link []string `toml:"link"`
}

// LibFlags are the flags of a block of lib, of all OSes or of one.
type LibFlags struct {
	// Cflags and Ldflags are flags of a compiler.
	Cflags  []string `toml:"cflags"`
	Ldflags []string `toml:"ldflags"`
}

// Lib returns the fake of a backend of modules.
func Lib() build.Backend {
	return build.Backend{
		Name:     LibName,
		Produces: []build.Result{build.Module},
		Config:   func() any { return new(LibConfig) },
		New: func(cfg any, _ build.Component, _ build.Common) (build.Build, error) {
			config, ok := cfg.(*LibConfig)
			if !ok {
				return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
			}

			if config.Module == "" {
				return nil, build.FieldErrorf("", "module is required for backend %q", LibName)
			}

			return libBuild{module: config.Module, link: config.Link}, nil
		},
		Incremental:    false,
		OutputFrom:     build.OutputNone,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           false,
		Cross:          false,
	}
}

// libBuild is a block of lib.
type libBuild struct {
	module string
	link   []string
}

// Links names the components of link.
func (b libBuild) Links() []string { return b.link }

// Preflight checks that the Tarantool of the build has headers.
func (b libBuild) Preflight(_ context.Context, c *build.Context) error {
	if c.Tarantool.IncludeDir == "" {
		return ErrNoHeaders
	}

	return nil
}

// Run makes the module, a copy of a program of the host.
func (b libBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err == nil {
		err = copyHostProgram(filepath.Join(c.Dirs.Modules, b.module+".so"))
	}

	return err
}

// BothConfig are the fields of a block of both.
type BothConfig struct {
	// Module is the module the build makes, as <module>.so; it has no dot.
	Module string `toml:"module"`
	// Bin is the program the build makes.
	Bin string `toml:"bin"`
	// Features and Profile are fields of a tool of both; Profile has no
	// space.
	Features []string `toml:"features"`
	Profile  string   `toml:"profile"`
}

// Both returns the fake of a backend of modules and programs. A block that
// names neither makes a module named as the component, but for a component
// of the deprecated kind = "executable", whose build makes a program so
// named.
func Both() build.Backend {
	return build.Backend{
		Name:     BothName,
		Produces: []build.Result{build.Module, build.Program},
		Config:   func() any { return new(BothConfig) },
		New: func(cfg any, component build.Component, _ build.Common) (build.Build, error) {
			config, ok := cfg.(*BothConfig)
			if !ok {
				return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
			}

			err := fileName("bin", config.Bin)

			switch {
			case err != nil:
				return nil, err
			case strings.Contains(config.Module, "."):
				return nil, build.FieldErrorf("module", "%q is not a module name", config.Module)
			case strings.Contains(config.Profile, " "):
				return nil, build.FieldErrorf("profile", "%q is not a profile name",
					config.Profile)
			}

			bld := bothBuild{module: config.Module, bin: config.Bin}

			if bld.module == "" && bld.bin == "" {
				if slices.Equal(component.Wants, []build.Result{build.Program}) {
					bld.bin = component.Name
				} else {
					bld.module = component.Name
				}
			}

			return bld, nil
		},
		Incremental:    false,
		OutputFrom:     build.OutputNone,
		Exclude:        []string{"/target/"},
		NoSourceLayout: false,
		Hook:           false,
		// It says it builds for another platform, as cargo does, and builds
		// for the host all the same: what it made fails the checks of the
		// target.
		Cross: true,
	}
}

// bothBuild is a block of both.
type bothBuild struct {
	module, bin string
}

// Produces names what the block makes.
func (b bothBuild) Produces() []build.Result {
	var results []build.Result

	if b.module != "" {
		results = append(results, build.Module)
	}

	if b.bin != "" {
		results = append(results, build.Program)
	}

	return results
}

// Programs names the program of the block.
func (b bothBuild) Programs() ([]string, bool) {
	if b.bin == "" {
		return nil, true
	}

	return []string{b.bin}, true
}

// Run makes the module and the program, copies of a program of the host.
func (b bothBuild) Run(_ context.Context, c *build.Context) error {
	if b.module != "" {
		err := copyHostProgram(filepath.Join(c.Dirs.Modules, b.module+".so"))
		if err != nil {
			return err
		}
	}

	if b.bin != "" {
		return copyHostProgram(filepath.Join(c.Dirs.Programs, b.bin))
	}

	return nil
}

// TreeConfig are the fields of a block of tree.
type TreeConfig struct {
	// Variables, Targets and BuildType are fields of a tool of trees.
	Variables map[string]string `toml:"variables"`
	Targets   []string          `toml:"targets"`
	BuildType string            `toml:"build_type"`
}

// Tree returns the fake of a backend that copies the files output lists
// itself, from the working directory of the build.
func Tree() build.Backend {
	return build.Backend{
		Name:     TreeName,
		Produces: build.Results(),
		Config:   func() any { return new(TreeConfig) },
		New: func(cfg any, _ build.Component, common build.Common) (build.Build, error) {
			if _, ok := cfg.(*TreeConfig); !ok {
				return nil, fmt.Errorf("%w: %T", build.ErrConfig, cfg)
			}

			if len(common.Output) == 0 {
				return nil, build.FieldErrorf("", "output is required for backend %q", TreeName)
			}

			return treeBuild{outputs: common.Output}, nil
		},
		Incremental:    false,
		OutputFrom:     build.OutputBackend,
		Exclude:        nil,
		NoSourceLayout: false,
		Hook:           false,
		Cross:          false,
	}
}

// treeBuild is a block of tree.
type treeBuild struct {
	outputs
}

// Programs names the programs of output: all the build makes.
func (b treeBuild) Programs() ([]string, bool) { return b.programs(), true }

// Run copies the files of output from the working directory into the
// directories of their types.
func (b treeBuild) Run(_ context.Context, c *build.Context) error {
	for _, file := range b.outputs {
		err := build.CopyFile(c.Dirs.OutputPath(file.To, filepath.Base(file.Path)),
			filepath.Join(c.Dirs.Cwd, file.Path))
		if err != nil {
			return fmt.Errorf("copying %s: %w", file.Path, err)
		}
	}

	return nil
}

// fileName checks that the field of value, when set, is a file name.
func fileName(field, value string) error {
	if value != "" && (strings.Contains(value, "/") || value == "." || value == "..") {
		return build.FieldErrorf(field, "%q is not a file name", value)
	}

	return nil
}

// noEmpty checks that the field of values has no empty entry.
func noEmpty(field string, values []string) error {
	if slices.Contains(values, "") {
		return build.FieldErrorf(field, "must not contain an empty entry")
	}

	return nil
}

// orDefault returns value, or fallback when it is empty.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}

// needTool checks that tool, which the backend named backend runs, is in
// the PATH of the build.
func needTool(c *build.Context, backend, tool string) error {
	_, err := c.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH", build.ErrNoTool,
			backend, tool)
	}

	return nil
}

// hostProgram is the program of the host the fakes copy as what they make:
// a native one every system has.
const hostProgram = "true"

// copyHostProgram copies hostProgram to dst.
func copyHostProgram(dst string) error {
	src, err := exec.LookPath(hostProgram)
	if err == nil {
		src, err = filepath.EvalSymlinks(src)
	}

	if err == nil {
		err = build.CopyFile(dst, src)
	}

	if err != nil {
		return fmt.Errorf("making %s: %w", dst, err)
	}

	return nil
}
