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
// [Tarantool] it is built against, the [Toolchain] of C, and the commands it
// runs ([Context.Command], [Context.Run], [Context.Output]), with the
// environment tt builds every component in. A Build may be a [Preflighter],
// which the core asks before any component is built, so that a missing tool
// fails the build before the first long compile; a [ProgramNamer], which
// names the programs an executable component makes; an [InputHasher], which
// adds to what an incremental build is skipped by. A backend copies what its
// tool built into the directories of the Context with [CopyFile].
//
// A backend whose [Backend.Hook] is set runs lifecycle hooks too: the core
// checks the block of a hook as it checks a build block, New given the zero
// Component, and runs its build around those of the components, in a
// Context whose Hook is set, which has no component and none of its
// directories.
//
// A module may also check the files the build lays out ([FileCheck]), by
// their suffix, whichever backend made them.
//
// The package depends on the standard library only, and not on the module
// contract (github.com/tarantool/tt/sdk), which depends on it.
package build
