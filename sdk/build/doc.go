// Package build is the contract of a build backend: what builds a component
// of a tt package whose build block names it (backend = "<name>"), with
// fields of the block of its own.
//
// A module contributes a [Backend] through the Backends of the
// github.com/tarantool/tt/sdk.Module its constructor returns. The core
// knows the backend by its Name. It decodes the fields of a build block that
// are not common to every backend into a fresh [Backend.Config] - strictly:
// a field the configuration has no place for is an error, unless another
// backend has it, and then the error says which one - and hands the result,
// the [Component] and the [Common] fields to [Backend.New], which checks
// them and returns the [Build]. The core does so when it checks a manifest,
// so a mistake in a build block is reported by every command that reads the
// manifest, not only by the build; an error of New that names a field is a
// [FieldError] ([FieldErrorf]), which the core reports with the path of the
// field in the manifest.
//
// When the build runs, the core gives the Build a [Context]: the component,
// the [Target] it is built for, the [Dirs] it reads and writes, the
// [Tarantool] it is built against, and the commands it runs
// ([Context.Command], [Context.Run], [Context.Output]), with the
// environment tt builds every component in. A Build may be a [Preflighter],
// which the core asks before any component is built, so that a missing tool
// fails the build before the first long compile; a [Producer], which says
// what types of results its block asks for; a [ProgramNamer], which names
// the programs it makes; an [InputHasher], which adds to what an incremental
// build is skipped by. Those that may run a command - Preflight, Inputs, and
// the Check of a [FileCheck] - are given the context of the build. The core
// finds these interfaces by type assertion, and refuses a Build with a
// method of one of their names but not of its signature, which it would
// never call ([Backend.ValidateBuild]).
//
// What a build makes is typed ([Result]) by the directory it goes to: a
// module into [Dirs.Output], data into [Dirs.Share], a program into
// [Dirs.Programs]. One build may make all three, and a backend says which
// types its builds make ([Backend.Produces]). A backend copies what its
// tool built into these directories with [CopyFile]. The files of the
// component - its Lua sources, by default - are laid out by the core, which
// a backend whose sources are no files of the package switches off
// ([Backend.NoSourceLayout]).
//
// A backend whose [Backend.Hook] is set runs lifecycle hooks too: the core
// checks the block of a hook as it checks a build block, New given the zero
// Component, and runs its build around those of the components, in a
// Context whose Hook is set, which has no component and none of its
// directories.
//
// A module may also check the files of a package ([FileCheck]) by their
// suffix, whichever backend made them: the files the components lay out and
// the data files their builds make.
//
// The package depends on the standard library only, and not on the module
// contract (github.com/tarantool/tt/sdk), which depends on it.
package build
