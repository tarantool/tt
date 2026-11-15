package sdk

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/output"
)

// Mount is a command a module contributes to tt, and where it goes.
type Mount struct {
	// Path is the command's parent, named by command names from the root down
	// and separated by spaces: "" is the root, "replicaset vshard" is the
	// vshard group under replicaset. Aliases do not name a parent. A group on
	// the path that no module provides is created, without a description.
	Path string
	// Cmd is the command. It is fresh - built by the constructor that returns
	// it, with no parent - and the core owns it once the constructor returns:
	// the core adds it to the tree and wraps its hooks to report the errors
	// they return.
	Cmd *cobra.Command
}

// Constructor builds a module's commands. The core calls it once per
// process, before tt is configured, so only [Services.Log] may be used while
// it runs; the other services are for the commands' hooks, which run after.
// A constructor must not change process state: it declares commands and
// their flags and nothing else.
//
// A module that contributes more than commands has a [ModuleConstructor]
// instead; the core takes either.
type Constructor func(Services) []Mount

// Module is what a module contributes to tt: commands, build backends and
// checks of the files of a package.
type Module struct {
	// Commands are the module's commands, as a [Constructor] returns them.
	Commands []Mount
	// Backends are the build backends the module provides: a component
	// whose build block names one is built by it. No two backends of a tt
	// have one name, whichever module provides them; the core has none of
	// its own, and those of tt are the standard module build's (make,
	// shell, c, lua-c, cargo, cmake, go and zig).
	Backends []build.Backend
	// FileChecks check the files of a package by their suffix, whatever
	// component made them: what the layout of a component copies and the
	// data files a build makes ([build.FileCheck]).
	FileChecks []build.FileCheck
}

// ModuleConstructor builds what a module contributes. The core calls it as
// it calls a [Constructor]: once per process, before tt is configured, so
// that only [Services.Log] may be used while it runs. It declares the
// module's commands, backends and file checks and changes nothing; the
// functions of a backend run when a manifest is checked or a component is
// built, once tt is configured.
type ModuleConstructor func(Services) Module

// ErrNotFound reports a thing tt looked for and did not find, such as a
// Tarantool executable. Services wrap it; test for it with errors.Is.
var ErrNotFound = errors.New("not found")

// Services are what the tt core offers a module.
//
// Only the core and [github.com/tarantool/tt/sdk/sdktest] implement Services;
// a module must not, since methods may be added in a minor release of the
// SDK. Log is valid at any time. Every other method is valid only once tt is
// configured - in a command's hooks - and panics before that with a message
// naming the module and the method. Services are safe for concurrent use.
type Services interface {
	// Log returns the module's logger: the process logger with a
	// module=<name> attribute. Whether debug records are shown is the
	// process's --verbose; test it with Logger.Enabled.
	Log() *slog.Logger
	// Tarantool returns the Tarantool the command should run: the one the
	// project bundles, when it has one, and the environment's otherwise. The
	// project is the one [Project.Dir] is in, as
	// [github.com/tarantool/tt/sdk/scope.FindProject] finds it, so a command
	// run in a subdirectory of a project runs the project's Tarantool, as
	// tt run does. When there is none the error wraps ErrNotFound.
	Tarantool() (Tarantool, error)
	// Integrity returns the integrity checks files are read through.
	Integrity() Integrity
	// Project returns the project the command works on.
	Project() Project
	// Confirm asks the user question on stderr and reads a yes or no answer
	// from stdin. When the user asked tt not to prompt (--no-prompt) it asks
	// nothing and returns fallback, which is therefore the answer that is
	// safe to assume.
	Confirm(question string, fallback bool) (bool, error)
	// Streams returns the process's standard streams and the printers bound
	// to them.
	Streams() Streams
	// ClusterConfig returns the cluster configuration of src as Tarantool 3
	// sees it, as an immutable snapshot, with the directory of the file it
	// was read from ([ClusterConfig.Dir]):
	//
	//   - for an application or a file, the cluster config file merged with
	//     the TT_* environment and with the etcd or Tarantool config storage
	//     the file names, in Tarantool's order of precedence, the storage
	//     read through the integrity checks;
	//   - for a storage, what the storage holds under the URI's prefix (or
	//     at its key= parameter), and nothing else.
	//
	// The configuration is not validated against Tarantool's schema. It
	// carries the Tarantool hierarchy, so [Instances] and [InstanceConfig]
	// read it. An application or a file that does not exist, an application
	// without a cluster configuration and a storage that holds nothing under
	// the prefix (or at the key) wrap ErrNotFound. A storage that cannot be
	// reached is another error.
	ClusterConfig(ctx context.Context, src ClusterSource) (ClusterConfig, error)
	// Rocks returns the rock servers tt writes.
	Rocks() Rocks
	// Exit ends the process as the core ends it for a command that returned
	// err: it reports err once and exits with the code tt returns for it
	// ([ExitCode], and ExitSystem for a failure of the system). Exit(nil)
	// exits ExitOK. It is for code that cannot return an error to the
	// command, such as a callback of an interactive prompt; a command that
	// can return its error must. Exit does not return.
	Exit(err error)
}

// Tarantool is a Tarantool executable.
type Tarantool interface {
	// Path returns the path to the executable.
	Path() string
	// Version returns the version the executable reports. It runs the
	// executable the first time and remembers the answer.
	Version() (TarantoolVersion, error)
}

// Integrity reads files through tt's integrity checks: a file that fails
// them cannot be opened.
type Integrity interface {
	// Open opens the file at path for reading, having checked it.
	Open(path string) (io.ReadCloser, error)
}

// Project is the directory a command works on, the project it is in, and
// the package the app.manifest.toml of the project describes, managed as tt
// package manages it.
//
// Only the core and [github.com/tarantool/tt/sdk/sdktest] implement Project,
// as they implement Services.
//
//nolint:interfacebloat // What tt package does to a project, one service as it is one group.
type Project interface {
	// Dir returns the directory of the command as an absolute path: the
	// working directory tt runs in. A module cannot declare -C; a command
	// mounted under a group of tt that declares -C for its subcommands, as
	// tt package does, gets the directory it names. No manifest is searched
	// for: the project Dir is in is [Project.Root].
	Dir() (string, error)
	// Root returns the project the command works on as an absolute path:
	// the project Dir is in, as the project commands of tt find it - the
	// nearest directory from Dir up with a project marker
	// ([github.com/tarantool/tt/sdk/scope.FindProject]), or Dir itself
	// under -C - and Dir when there is none. When it is above Dir, tt says
	// once in the process which project the command works on, as tt package
	// does. The other methods of Project work on Root.
	Root() (string, error)
	// Manifest returns what the app.manifest.toml of Root declares, parsed
	// as tt package parses it and not checked: its warnings are not shown,
	// the commands of tt package show them. A project without a manifest is
	// an error that wraps ErrNotFound; a manifest that does not parse is an
	// error that names it.
	Manifest() (Manifest, error)
	// AddComponent declares component in the app.manifest.toml of Root. The
	// tables of the component are appended to the manifest, every other
	// line and comment kept, and the manifest that makes is parsed and
	// checked as tt package build checks it, the backends of this tt
	// included, before it is written in its place. A name the manifest
	// declares already, a component that fails the check and a project
	// without a manifest, whose error wraps ErrNotFound, are errors, and
	// nothing is written.
	//
	// The lock is not resolved, and no Tarantool is needed: the lock is
	// stale afterwards, as after an edit of the manifest by hand, until
	// [Project.Resolve] or a build resolves it again.
	AddComponent(ctx context.Context, component Component) error
	// UpdateComponent changes the component the manifest of Root declares
	// as update says: a field there has its value replaced where it is
	// written, a new one goes after the last field of its table, a removed
	// one takes its lines, and every other line and comment stays. The
	// manifest is checked and written as AddComponent writes it, and the
	// lock is not resolved. A component the manifest does not declare is an
	// error that wraps ErrNotFound; a field written in a form tt does not
	// edit, inside an inline table, is an error that names it, to be edited
	// by hand; nothing is written then.
	UpdateComponent(ctx context.Context, update ComponentUpdate) error
	// RemoveComponent removes the component name from the manifest of Root:
	// its tables, with the blank lines above them, and the keys that
	// declare it elsewhere; comments stay. The manifest is checked and
	// written as AddComponent writes it - a product that names the
	// component fails the check - and the lock is not resolved. Nothing of
	// the directory of the component is removed. A component the manifest
	// does not declare is an error that wraps ErrNotFound.
	RemoveComponent(ctx context.Context, name string) error
	// Resolve resolves app.manifest.lock of Root against its manifest, as
	// tt package resolve does: every version the lock chose is held, the
	// manifest is not changed and nothing is installed. It is what a
	// command runs after AddComponent, UpdateComponent or RemoveComponent
	// when the lock is to be fresh. A tt with no Tarantool resolves as
	// tt package build resolves a stale lock, without one: a rockspec that
	// asks for the version of Tarantool is evaluated with none. A project
	// without a manifest is an error that wraps ErrNotFound. The warnings of
	// the resolution are logged.
	Resolve(ctx context.Context, opts ResolveOptions) (ResolveChange, error)
	// ReadLock returns what app.manifest.lock of Root pins, and whether it
	// is fresh for the manifest, reading the lock and resolving nothing. A
	// project without a manifest or a lock is an error that wraps
	// ErrNotFound; a lock that does not parse is an error that names it.
	ReadLock() (LockFile, error)
	// StateDir returns the directory of the state of Root on this machine,
	// .tarantool, as an absolute path: tt binary installs into the project
	// scope there, and its bin/ comes first in the PATH of the builds of the
	// project and of the shell .env/activate.sh activates. It is not
	// created, nor is it committed with the project.
	StateDir() (string, error)
	// PrepareEnvironment writes .env/activate.sh and .env/deactivate.sh of
	// Root when they are missing, as the first tt binary install into the
	// project scope does: sourced, they put bin/ of [Project.StateDir] on
	// the PATH of a shell and take it off. Scripts that are there are the
	// user's, and are kept.
	PrepareEnvironment() error
	// AddDependency declares dep in the app.manifest.toml of Root and
	// re-resolves app.manifest.lock, as tt package add does,
	// holding every version the lock already chose: only dep and what it
	// requires can move. Nothing is installed; [Project.Install] does that.
	//
	// When the table dep goes to declares the name already, the change
	// reports Existed and the constraint and the registry the declaration
	// had, and the declaration is set to dep: its constraint is rewritten to
	// dep's, "*" for none; for a dep with a Registry its registry is set
	// too, a short declaration becoming the long form; for a dep without one
	// a registry it names is kept. The other fields of the declaration, a
	// source say, are kept, and so is a declaration that says what dep says
	// already. A command that keeps a declaration someone chose reads it in
	// [Project.Manifest] first, and resolves with [Project.Resolve] instead.
	// A name declared in the other table as well stays there, with a
	// warning. A path dependency, which has no version, is refused. The
	// manifest is rewritten in place, every other line and comment kept, and
	// before the resolution: when the resolution fails the declaration stays
	// and the error says so.
	//
	// The warnings of the manifest and of the resolution are logged. A
	// project without a manifest, an invalid manifest or constraint, and a
	// closure no server can satisfy are errors; so is a tt with no Tarantool,
	// whose version the rockspecs are evaluated for, and that error wraps
	// ErrNotFound.
	AddDependency(ctx context.Context, dep Dependency) (DependencyChange, error)
	// RemoveDependency removes the rock name from [dependencies] and
	// [dev_dependencies] of the manifest of Root and re-resolves the lock
	// without it, as tt package remove does: every other version is held,
	// and the rocks only it required drop out of the closure. Nothing is
	// removed from .rocks. The change reports the constraint and the
	// registry of the declaration removed, and the moves. A name the
	// manifest does not declare there is an error that wraps ErrNotFound;
	// one a component declares stays, with a warning. A tt with no
	// Tarantool fails as AddDependency fails.
	RemoveDependency(ctx context.Context, name string, opts ResolveOptions) (
		DependencyChange, error)
	// Install materializes .rocks/ of Root strictly from its lock, as
	// tt package fetch does for the default product: the locked closure of
	// the product and the development closure are installed at the versions
	// the lock pins, other versions of those rocks are removed, and nothing
	// is resolved and no component of the project is built. A project
	// without a manifest or a lock, and a lock with no closure for the
	// product, are errors. A tt with no Tarantool installs the rocks that
	// need none, and fails on one compiled against the headers of
	// Tarantool. The warnings are logged.
	Install(ctx context.Context, opts InstallOptions) error
	// Build builds the package of Root, or the components opts names, with
	// the code of tt package build and as it builds them: the preflight,
	// pre_build, the lock gated - resolved when stale, an error under
	// opts.Locked - .rocks materialized, the builds of the components, their
	// layout, the checks of the files, version.lua, the install of the rock
	// of the package into .rocks and post_build. Build with
	// Components: []string{"calc"} is tt package build calc. What the tools
	// of the builds print is shown as tt package build shows it, on the
	// standard streams of the process. The error is the one tt package build
	// fails with, with its exit code ([ExitCode]): ExitSystem for a build
	// that failed. A tt with no Tarantool builds as tt package build does
	// without one.
	Build(ctx context.Context, opts BuildOptions) (BuildResult, error)
	// BuildContext returns the Context the build of a component would run
	// in, and the build its backend makes of its block, building nothing:
	// for a command of the module of the backend that runs its tools in
	// the environment a build has - PATH with .tarantool/bin first, the
	// env of the block, the TT_* variables - as tt wasm fetch and bind do.
	// The manifest is read and checked as Build checks it, the version of
	// the package is derived, TT_BUILD_DIR (Dirs.Work) is created, and the
	// lock is read and not resolved: LockedVersion answers as during the
	// preflight of a build, from a lock that is fresh. No hook and no
	// backend runs; the directories of the stage and .rocks may not exist.
	// A component the product does not build, or with no build block, is an
	// error.
	BuildContext(ctx context.Context, opts BuildContextOptions) (
		*build.Context, build.Build, error)
}

// Rocks are the rock servers tt writes, as tt package index writes one.
//
// Only the core and [github.com/tarantool/tt/sdk/sdktest] implement Rocks,
// as they implement Services.
type Rocks interface {
	// Index makes the directory dir a rock server of the rocks and the
	// rockspecs it holds: it writes the LuaRocks manifests, as luarocks-admin
	// make_manifest does, which LuaRocks reads, and the rock index of tt,
	// tt-rocks-index.json, which tt reads first, so that the lock of a
	// project that declares a rock from dir pins the sha256 of its files.
	// An index already there is updated as tt package index updates it.
	// Files that are neither rocks nor rockspecs are left out.
	Index(ctx context.Context, dir string, opts IndexOptions) error
}

// Streams are the process's standard streams.
type Streams interface {
	// IO returns stdin, stdout and stderr.
	IO() output.Streams
	// Printer returns a Printer writing to stdout in format, with every
	// format the core encodes (such as YAML) and the core's check of whether
	// stdout is a terminal.
	Printer(format output.Format) (*output.Printer, error)
}
