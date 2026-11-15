package build

import (
	"context"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
)

// Target is the platform a component is built for, as GOOS and GOARCH name
// it: the machine tt runs on, since tt does not build for another.
type Target struct {
	OS   string
	Arch string
}

// ModuleExt returns the extension of a compiled module Tarantool loads on
// the target: ".dylib" on darwin, ".so" elsewhere.
func (t Target) ModuleExt() string {
	if t.OS == "darwin" {
		return ".dylib"
	}

	return ".so"
}

// Dirs are the directories of a build, all of them absolute. The core
// creates Work, Modules, Share and Programs before the build runs and takes
// as made what the build leaves in Modules, Share and Programs, each by its
// type ([Result]) - a type its backend does not produce fails the build
// ([ErrNotProduced]) - and anything else there that no build made is
// removed. The build of a hook has no Component, Work, Modules, Share and
// Programs: they are empty.
type Dirs struct {
	// Project is the root of the project the component is of: the package,
	// or a path dependency or a source rock being built.
	Project string
	// Component is the directory of the component.
	Component string
	// Cwd is the working directory of the build: the cwd of its block, or
	// Component.
	Cwd string
	// Work is the directory the build keeps its intermediate files in,
	// between builds too, out of the sources: emptied when the build is
	// Fresh.
	Work string
	// Modules is where the build puts the modules it makes ([Module]): lib/
	// of the stage of the package, under the namespace of the component,
	// TT_OUTPUT_DIR.
	Modules string
	// Share is where the build puts the data files it makes ([Data]): what
	// Tarantool reads as files rather than loads as modules, such as .wasm,
	// under the namespace of the component, beside its Lua sources.
	Share string
	// Programs is where the build puts the programs it makes ([Program]):
	// bin/ of the stage of the package, which is installed into .rocks/bin
	// of the project, where tt instance finds them. The programs of the
	// other components are there too.
	Programs string
	// State is the directory of the state of the build of the project, the
	// same for every component of it.
	State string
	// Tools is the directory of the tools of the project, .tarantool/bin,
	// which is first in the PATH of the commands of the build when it
	// exists: what the build runs, not what it makes.
	Tools string
	// Tree is the rocks tree the dependencies of the project are installed
	// into.
	Tree string
}

// Of returns the directory the results of the type result go to: Modules,
// Share or Programs; empty for no type of [Results].
func (d Dirs) Of(result Result) string {
	switch result {
	case Module:
		return d.Modules
	case Data:
		return d.Share
	case Program:
		return d.Programs
	default:
		return ""
	}
}

// Tarantool is the Tarantool a component is built against. How a native
// module is compiled and linked against it - the compiler, its flags - is
// the backend's: the Context says which Tarantool, not how to build for it.
type Tarantool struct {
	// Prefix is its install prefix, TT_TARANTOOL_PREFIX; empty when tt does
	// not know it.
	Prefix string
	// IncludeDir is the directory of its C headers, module.h and lua.h, as
	// #include <module.h> finds them, TT_TARANTOOL_INCLUDE_DIR: include/tarantool
	// under Prefix, and empty when module.h is not there.
	IncludeDir string
	// Version is its version; empty when tt does not know it.
	Version string
	// Executable is its tarantool, the program tt found, for a build that
	// runs it to check what it made - that a module loads, say; empty when
	// tt found none. A build must not need it: tt builds without a
	// Tarantool what needs none.
	Executable string
}

// Host runs the commands of a build for a [Context]. Only the core and
// [github.com/tarantool/tt/sdk/build/buildtest] implement it, since methods
// may be added to it; a backend calls the methods of Context.
type Host interface {
	// Command returns the command name with args, run in [Dirs.Cwd], with
	// the environment of the build.
	Command(ctx context.Context, name string, args ...string) *exec.Cmd
	// Run runs cmd, showing what it prints as tt shows it.
	Run(cmd *exec.Cmd) error
	// Output runs cmd and returns what it printed to stdout; its stderr is
	// shown as Run shows it.
	Output(cmd *exec.Cmd) ([]byte, error)
	// LookPath finds the program name in the PATH of the build, and only
	// there.
	LookPath(name string) (string, error)
	// LockedVersion returns the version of the rock the lock of the project
	// pins.
	LockedVersion(rock string) (string, bool)
}

// Context is what a build is given to run: the facts of the build and the
// way to run its commands. The core makes one per component and build
// ([NewContext]).
type Context struct {
	// Component is the component built.
	Component Component
	// Target is the platform it is built for.
	Target Target
	// Dirs are the directories of the build.
	Dirs Dirs
	// Tarantool is the Tarantool it is built against.
	Tarantool Tarantool
	// Env are the variables of the build: those of its block, and the TT_*
	// ones tt sets for every build, which win.
	Env map[string]string
	// Fresh asks the build to drop what it kept in Work and to build from
	// scratch, as tt package build --rebuild does, and as tt does for a
	// component whose build block changed since its last build.
	Fresh bool
	// Locked is set when the lock of the project is not to be resolved
	// again (--locked): a tool of the build must not move a version either.
	Locked bool
	// Static is the static of the build block ([Common.Static]).
	Static bool
	// Args are the arguments the command that started the build passed
	// through to the build of this component, for its tool: those after --
	// of tt wasm build calc -- -opt=2, which a module hands the core with
	// sdk.BuildOptions.Args. None in a build of tt package build. They are
	// inputs of an incremental build: a build with other Args is not
	// skipped.
	Args []string
	// Hook is set for the build of a lifecycle hook ([Backend.Hook]), which
	// runs around the builds of all the components rather than for one:
	// Component is the zero one, Dirs has no Component, Work, Modules, Share
	// and Programs, and Env has no TT_OUTPUT_DIR, TT_COMPONENT_NAME and
	// TT_BUILD_DIR. The files output lists are not copied for a hook.
	Hook bool
	// Log is the logger of the build: tt's, as the logger of a module is,
	// with a backend=<name> attribute; tt -V shows its debug records.
	Log *slog.Logger
	// Warn shows a warning to the user.
	Warn func(string)

	host Host
}

// NewContext returns a copy of facts that runs its commands with host.
func NewContext(facts Context, host Host) *Context {
	facts.host = host

	return &facts
}

// Command returns the command name with args, run in [Dirs.Cwd], with
// cmd.Env set to the environment of the build: that of tt, the env of the
// build block, PATH with .tarantool/bin of the project first, and the TT_*
// variables last, so that the block cannot change them. name is looked up in
// that PATH and nowhere else, as [Context.LookPath] looks: a name it does not
// have makes a command that fails to start with an error wrapping
// exec.ErrNotFound. A backend may append to cmd.Env, a variable it appends
// winning, and read it with [LookupEnv].
func (c *Context) Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	return c.mustHost().Command(ctx, name, args...)
}

// Run runs cmd as tt runs the commands of a build: what it prints is shown
// as it runs with tt -V, and only when it fails otherwise.
func (c *Context) Run(cmd *exec.Cmd) error {
	return c.mustHost().Run(cmd)
}

// Output runs cmd as Run does and returns what it printed to stdout, which
// is not shown: for a tool that reports in JSON there.
func (c *Context) Output(cmd *exec.Cmd) ([]byte, error) {
	return c.mustHost().Output(cmd)
}

// LookPath finds the program name in the PATH of the build, which has
// .tarantool/bin of the project first, and nowhere else: a PATH the env of
// the build block sets is the PATH of the build, the PATH of tt is not looked
// at then. A name it does not have is an error wrapping exec.ErrNotFound,
// which a [Preflighter] reports as [ErrNoTool]. A name with a slash is a path,
// relative to [Dirs.Cwd], that must be an executable file.
func (c *Context) LookPath(name string) (string, error) {
	return c.mustHost().LookPath(name)
}

// LockedVersion returns the version of the rock the lock of the project
// pins, and whether it pins one: for a tool whose version follows that of a
// rock. The closure is that of the product built and the development one.
// During [Preflighter.Preflight], before the build has gated its lock, it is
// that of the lock on disk when the lock is fresh, and no rock is pinned
// when the lock is stale or missing: the build will resolve it.
func (c *Context) LockedVersion(rock string) (string, bool) {
	return c.mustHost().LockedVersion(rock)
}

// LookupEnv returns the value of key in environ, a list of key=value pairs
// as cmd.Env holds them, and whether it is there: the last one, which the
// command gets.
func LookupEnv(environ []string, key string) (string, bool) {
	for _, entry := range slices.Backward(environ) {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value, true
		}
	}

	return "", false
}

// mustHost returns the host of c. A Context made other than by NewContext
// has none, which is a mistake of whoever made it.
func (c *Context) mustHost() Host {
	if c.host == nil {
		panic("build: a Context not made by NewContext runs no command")
	}

	return c.host
}
