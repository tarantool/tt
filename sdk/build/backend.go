package build

import (
	"context"
	"errors"
	"fmt"
)

// Result is a type of what a build makes. Each type has a directory of the
// build ([Dirs.Of]), which the rock of the package installs to its place in
// the rocks tree. One build may make results of every type.
type Result string

const (
	// Module is a file Tarantool loads as a module, a compiled one mostly:
	// it goes to [Dirs.Output], lib/<namespace> of the rock.
	Module Result = "module"
	// Data is a file Tarantool reads rather than loads, such as .wasm: it
	// goes to [Dirs.Share], share/<namespace> of the rock, beside the Lua
	// sources of the component.
	Data Result = "data"
	// Program is a program the instances of an application run as external
	// processes: it goes to [Dirs.Programs], bin of the rock.
	Program Result = "program"
)

// Results are the result types, in the order the core lists them in.
func Results() []Result {
	return []Result{Module, Data, Program}
}

// OutputFrom says where the files the output field of a build block lists
// are taken from, and so whether a backend has the field at all.
type OutputFrom int

const (
	// OutputNone is a backend without the output field: it puts what it
	// made into the directories of the Context itself, and a build block of
	// it that lists output is refused.
	OutputNone OutputFrom = iota
	// OutputCwd has the core copy the files output lists, relative to the
	// working directory of the build, into the directory of the type of
	// each ([OutputFile.To]) after the build succeeded - and before an
	// incremental build that was skipped is taken as built, since the stage
	// may no longer hold them.
	OutputCwd
	// OutputBackend leaves the files output lists to the backend: it reads
	// them from [Common.Output] and copies them itself, from where it built
	// them, into the directory of the type of each.
	OutputBackend
)

// Backend is a build backend: what builds a component whose build block
// names it.
//
// The core calls Config and New when it checks a manifest - by every
// command that reads one, before anything is built - and may call them more
// than once for one block, so neither may have side effects. A backend that
// is not well formed ([Backend.Validate]) is refused when tt starts, and
// buildtest refuses it alike.
type Backend struct {
	// Name is the value of backend in a build block that selects it: a
	// lowercase letter, then lowercase letters, digits and dashes. No two
	// backends of a tt have one name.
	Name string
	// Produces are the types of what the builds of the backend make, each
	// once. A block that asks for another type is refused: a file of
	// output whose to names it, a build that says it makes it ([Producer]).
	// A build that makes another type all the same fails, with
	// [ErrNotProduced]: what it left in the directory of that type, or the
	// program it names ([ProgramNamer]); what an earlier build, of another
	// backend say, left there is not taken as made, and leaves the package.
	Produces []Result
	// Config returns a fresh, empty configuration: a pointer to a struct
	// whose fields are the fields of the build block the backend has, named
	// by their toml tags (the field name when there is none), each a
	// string, a boolean, a number, a slice, a map with string keys, a
	// struct of such fields, a pointer to one of these, or any. The common
	// fields of every block - backend, cwd, env, output, incremental and
	// static - are the core's and are not in it ([Common]).
	Config func() any
	// New checks cfg, the configuration Config returned with the fields of
	// the build block decoded into it, for the component c, whose block has
	// the common fields common, and returns the build. An error of a field
	// is a [FieldError], naming the field as the block does
	// ([FieldErrorf]); any other error is reported for the block.
	New func(cfg any, c Component, common Common) (Build, error)
	// Incremental allows incremental = true: the core then skips the build
	// of a component whose inputs - the build block and the files of the
	// component, those its .gitignore files ignore left out - are those of
	// its last build. A build that reads other inputs names them
	// ([InputHasher]).
	Incremental bool
	// OutputFrom says whether the backend has the output field, and who
	// copies what it lists.
	OutputFrom OutputFrom
	// Exclude are gitignore patterns of files of the component the layout
	// never takes, whatever its include says: what the backend's tool
	// leaves in the component's directory, such as target/ of cargo.
	Exclude []string
	// NoSourceLayout has the layout take no file of the component unless
	// the include of the component names it: the default include, the Lua
	// sources and the prebuilt modules, is off. For a backend whose sources
	// are no files of the package, such as go, whose test fixtures would
	// otherwise be laid out.
	NoSourceLayout bool
	// Hook lets a lifecycle hook ([hooks.pre_build], [hooks.post_build])
	// name the backend; a hook may name no other. New checks the block of a
	// hook too, given the zero Component, and the build runs in a Context
	// whose Hook is set ([Context.Hook]).
	Hook bool
}

// ErrNoTool reports a program a build runs that is not in its PATH. A
// [Preflighter] wraps it, so that a missing tool is told from a build that
// failed.
var ErrNoTool = errors.New("build tool not found")

// ErrNotProduced reports a build that made a file of a type its backend does
// not produce ([Backend.Produces]): one it left in the directory of that type
// ([Dirs.Of]), or a program it named ([ProgramNamer]). The core fails the
// build with it, and buildtest the run of the build.
var ErrNotProduced = errors.New("the build made what its backend does not produce")

// Component is the component a build block builds.
type Component struct {
	// Name is the name of the component.
	Name string
	// Path is the directory of the component, relative to the project.
	Path string
	// Namespace is the namespace the modules and the data files of the
	// component go under; empty is a flat layout.
	Namespace string
	// Package is the name of the package of the component.
	Package string
	// Expects are the types of what the manifest says the build makes, for
	// a manifest that says it: kind of manifest_version 0.1, deprecated,
	// is [Program] for kind = "executable" and [Module] and [Data] for
	// kind = "module". Nil when the manifest says nothing, as a manifest is
	// to: the build makes what its block asks for. The core checks the
	// build against them; a backend that picks what to build when its block
	// does not say follows them - cargo builds the binary named after the
	// component then.
	Expects []Result
}

// Common are the fields every build block has, whatever its backend.
type Common struct {
	// Cwd is the working directory of the build, relative to the project;
	// empty is the directory of the component.
	Cwd string
	// Env are the variables the build block adds to the environment of the
	// commands of the build.
	Env map[string]string
	// Output are the files the build makes, as output lists them; see
	// [Backend.OutputFrom].
	Output []OutputFile
	// Static asks for what the build makes to need nothing of the machine
	// it is installed on. The core checks that it does, after the build.
	Static bool
}

// OutputFile is a file of the output field of a build block.
type OutputFile struct {
	// Path is the file, as output names it: relative to the working
	// directory of the build for [OutputCwd], to where the backend builds
	// for [OutputBackend].
	Path string
	// To is the type of the file, which says where it goes, by its base
	// name: [Module] for to = "lib", [Data] for to = "share", [Program] for
	// to = "bin". An entry that says no to - a plain string - is of the one
	// type of a backend whose [Backend.Produces] has one: a backend of data
	// files only lists them with output = "dist/calc.wasm". Of another
	// backend it is a [Module], and a [Program] for a component of
	// kind = "executable" (manifest_version 0.1, deprecated).
	To Result
}

// FieldError is an error of a field of a build block. Field is the path of
// the field from the block, as the block writes it - "module", "env.CC",
// "platforms.linux.cflags", "sources[1]" - and empty for the block itself;
// the core reports it with the path of the block in the manifest in front.
type FieldError struct {
	// Field is the path of the field from the build block.
	Field string
	// Msg says what is wrong with it.
	Msg string
}

// Error renders the error as "<field>: <msg>", or the message alone for the
// block itself.
func (e *FieldError) Error() string {
	if e.Field == "" {
		return e.Msg
	}

	return e.Field + ": " + e.Msg
}

// FieldErrorf returns a [FieldError] of field with a message formatted as
// fmt.Sprintf formats it.
func FieldErrorf(field, format string, a ...any) error {
	return &FieldError{Field: field, Msg: fmt.Sprintf(format, a...)}
}

// Build is a checked build block, ready to run.
type Build interface {
	// Run builds the component of c: in [Dirs.Cwd], each file it makes into
	// the directory of its type - [Dirs.Output] for a module, [Dirs.Share]
	// for data, [Dirs.Programs] for a program - keeping what it may reuse
	// next time in [Dirs.Work]. The core takes as made what Run left in
	// these directories, by their types, and what output lists
	// ([Backend.OutputFrom]); a file in the directory of a type the backend
	// does not produce fails the build ([ErrNotProduced]).
	Run(ctx context.Context, c *Context) error
}

// Preflighter is a Build that checks what it needs before any component is
// built: a tool in PATH, a target the platform has. The core asks every
// component it is about to build before it builds the first one, and
// before the version of the package is known: the directories of the
// Context may not exist yet, and TT_VERSION is empty. It runs before the
// lock is gated, too, so that it fails before a hook and the resolution
// run: [Context.LockedVersion] answers from the lock on disk when that lock
// is fresh, and pins nothing when it is stale or missing, which the build
// resolves afterwards - under --locked such a lock fails the build, so
// there the answer is exact. ctx is that of the
// build: a command Preflight runs is made with it ([Context.Command]), so
// that it stops when the build is cancelled.
//
// The core finds a Preflighter by a type assertion; a method Preflight of
// another signature is refused ([Backend.ValidateBuild]) rather than never
// called. A backend may assert it at compile time as well:
//
//	var _ build.Preflighter = myBuild{}
type Preflighter interface {
	Preflight(ctx context.Context, c *Context) error
}

// Producer is a Build that says what its block asks it to make: the types
// of the results it makes at least, each of them among
// [Backend.Produces]. The core checks them against the kind of a
// component of manifest_version 0.1 before anything is built. A Build that
// is no Producer may make any type its backend produces.
type Producer interface {
	Produces() []Result
}

// ProgramNamer is a Build that names the programs it makes. The core
// checks with them that no two components make one program, and tells by
// them which component makes a program an instance runs. When the list is
// exhaustive the core takes as programs what the build names and nothing
// else; otherwise it adds what the build wrote into [Dirs.Programs].
type ProgramNamer interface {
	Programs() (names []string, exhaustive bool)
}

// InputHasher is a Build of a backend that allows incremental builds whose
// build reads more than the build block and the files of the component: a
// version of a tool, a file outside the component. The core adds what
// Inputs returns to the inputs it compares with those of the last build.
// ctx is that of the build, for the commands Inputs runs, such as
// zig version.
type InputHasher interface {
	Inputs(ctx context.Context, c *Context) ([]string, error)
}

// Cleaner is a Build that removes what its builds leave outside the
// directories of the core, as tt package clean cleans the component: files
// it generates in the directory of the component, the files of its output
// that its tool makes in the working directory of the build, a cache of its
// own. The core asks every component it cleans before it removes anything of
// its own, so that Clean may still read [Dirs.Work], and then removes
// [Dirs.Work], the records of the component, its files in the stage and the
// rock of the package in [Dirs.Tree]; for a clean of the whole project it
// removes all of [Dirs.State]. What is anywhere else - the directory of the
// component, [Dirs.Cwd], [Dirs.Tools] - the core never removes, as it cannot
// tell what of it the build made from what the user wrote: the files output
// lists may be prebuilt ones the project keeps. So Clean removes what its
// builds made there, and nothing else: a file its tool would make again. It
// runs for a component that was never built too, so a file that is not there
// is no error.
//
// c is a Context as that of a build of the component - the same Dirs, Env
// and PATH - but for [Context.Args], which are empty, and Fresh, Locked and
// Static, which are false; its directories may not exist, and the version
// of the package is empty when it cannot be derived. ctx is that of the
// clean: a command Clean runs is made with it ([Context.Command]). The core
// runs Clean under the lock of the project, as it runs Run. An error fails
// the clean, with exit code 2, before the core removes anything.
//
// The core finds a Cleaner by a type assertion; a method Clean of another
// signature is refused ([Backend.ValidateBuild]) rather than never called.
type Cleaner interface {
	Clean(ctx context.Context, c *Context) error
}

// FileCheck checks the files of a package whose names end with Suffix,
// whatever component and backend made them: the files the layout of every
// component copies into the package, and the data files ([Data]) the build
// of every component makes in [Dirs.Share], those of a build skipped as up
// to date included. Modules and programs are not given to it: the core
// checks them itself.
type FileCheck struct {
	// Suffix is the end of the names of the files Check is given: ".wasm".
	Suffix string
	// Check checks the file at path, an absolute path in the stage of the
	// package. A warning is shown and the build goes on; an error fails the
	// build, with exit code 1, and the warnings of the other files are not
	// shown. Both are shown as they are, so they name the file. ctx is that
	// of the build: a check that runs a tool runs it with ctx, so that it
	// stops when the build is cancelled.
	Check func(ctx context.Context, path string) (warning string, err error)
}

// SupportedOS lists the operating systems tt builds for, as GOOS names
// them: linux, darwin.
func SupportedOS() []string {
	return []string{"linux", "darwin"}
}
