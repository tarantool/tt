package build

import (
	"context"
	"errors"
	"fmt"
)

// Component kinds, as [components.<name>].kind names them.
const (
	// KindModule is a component whose files are laid out under its
	// namespace in the rocks tree: Lua sources and compiled modules.
	KindModule = "module"
	// KindExecutable is a component whose build makes programs, which go to
	// the bin directory of the rocks tree.
	KindExecutable = "executable"
)

// OutputFrom says where the files the output field of a build block lists
// are taken from, and so whether a backend has the field at all.
type OutputFrom int

const (
	// OutputNone is a backend without the output field: it puts what it
	// made into [Dirs.Output] itself, and a build block of it that lists
	// output is refused.
	OutputNone OutputFrom = iota
	// OutputCwd has the core copy the files output lists, relative to the
	// working directory of the build, into [Dirs.Output] after the build
	// succeeded - and before an incremental build that was skipped is taken
	// as built, since the stage may no longer hold them.
	OutputCwd
	// OutputBackend leaves the files output lists to the backend: it reads
	// them from [Common.Output] and copies them itself, from where it built
	// them.
	OutputBackend
)

// Backend is a build backend: what builds a component whose build block
// names it.
//
// The core calls Config and New when it checks a manifest - by every
// command that reads one, before anything is built - and may call them more
// than once for one block, so neither may have side effects.
type Backend struct {
	// Name is the value of backend in a build block that selects it: a
	// lowercase letter, then lowercase letters, digits and dashes. No two
	// backends of a tt have one name.
	Name string
	// Kinds are the kinds of the components the backend builds:
	// [KindModule], [KindExecutable] or both. A component of another kind
	// is refused.
	Kinds []string
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

// Component is the component a build block builds.
type Component struct {
	// Name is the name of the component.
	Name string
	// Kind is the kind of the component: [KindModule] or [KindExecutable].
	Kind string
	// Path is the directory of the component, relative to the project.
	Path string
	// Namespace is the namespace the files of a module component are laid
	// out under; empty is a flat layout, and an executable component has
	// none.
	Namespace string
	// Package is the name of the package of the component.
	Package string
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
	Output []string
	// Static asks for what the build makes to need nothing of the machine
	// it is installed on. The core checks that it does, after the build.
	Static bool
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
	// Run builds the component of c: in [Dirs.Cwd], into [Dirs.Output]
	// (and, as the backend needs, [Dirs.Share] and [Dirs.Bin]), keeping
	// what it may reuse next time in [Dirs.Work]. The core takes as made
	// what Run left in the directories it was given, and what output lists
	// ([Backend.OutputFrom]).
	Run(ctx context.Context, c *Context) error
}

// Preflighter is a Build that checks what it needs before any component is
// built: a tool in PATH, a target the platform has. The core asks every
// component it is about to build before it builds the first one.
type Preflighter interface {
	Preflight(c *Context) error
}

// ProgramNamer is a Build of an executable component that names the
// programs it makes. The core checks with them that no two components make
// one program, and tells by them which component an instance runs. When
// the list is exhaustive the core takes as made what the build names and
// nothing else; otherwise it adds what the build wrote into [Dirs.Output].
type ProgramNamer interface {
	Programs() (names []string, exhaustive bool)
}

// InputHasher is a Build of a backend that allows incremental builds whose
// build reads more than the build block and the files of the component: a
// version of a tool, a file outside the component. The core adds what
// Inputs returns to the inputs it compares with those of the last build.
type InputHasher interface {
	Inputs(c *Context) ([]string, error)
}

// FileCheck checks the files the build lays out whose names end with
// Suffix, whatever made them.
type FileCheck struct {
	// Suffix is the end of the names of the files Check is given: ".wasm".
	Suffix string
	// Check checks the file at path. A warning is shown and the build goes
	// on; an error fails the build.
	Check func(path string) (warning string, err error)
}

// SupportedOS lists the operating systems tt builds for, as GOOS names
// them: linux, darwin.
func SupportedOS() []string {
	return []string{"linux", "darwin"}
}
