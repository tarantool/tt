package pack

import (
	"errors"

	"github.com/tarantool/tt/sdk"
)

// File and directory names pack reads from the project and writes into the
// archive. The archive-side names are the reserved set tt owns: [package]
// include and license_files entries may not collide with them.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
	versionFileName  = "VERSION"
	runtimeDirName   = "_runtime"
	rocksDirName     = ".rocks"
	buildDirName     = "_build"
	// packSubDir is the _build subdirectory the archive is produced in.
	packSubDir = "pack"
	// archiveExt is the tt-native archive extension. The payload is tar+zstd.
	archiveExt = ".tt"
)

var (
	// errReservedName reports an include or license_files entry that would land
	// on a name tt owns inside the archive.
	errReservedName = errors.New("entry collides with a reserved archive name")
	// errNoRuntime reports that no Tarantool or tt satisfying [platform] could
	// be found to bundle into _runtime/.
	errNoRuntime = errors.New("no runtime available to bundle")
	// errBadConstraint reports a [platform] constraint tt cannot parse.
	errBadConstraint = errors.New("unparseable version constraint")
	// errFlatNamespace reports a --without-deps pack of a package that lays
	// files flat in the rocks tree, where its own files cannot be separated
	// from its dependencies'.
	errFlatNamespace = errors.New("cannot pack a flat namespace without deps")
	// errMissingInclude reports an include or license_files entry that matched
	// nothing on disk.
	errMissingInclude = errors.New("no such file")
	// errEscapingPath reports an include or license_files entry that resolves
	// outside the project directory.
	errEscapingPath = errors.New("path escapes the project directory")
)

// stateErrorf wraps a formatted error as a usage or state failure
// (sdk.ExitFailure), the code tt package build uses too, so the two commands
// are indistinguishable to a CI script.
//
// Pack defines no code of its own beyond this one: a build backend failure
// exits 2, but that error is raised inside cli/manifest/build and passes
// through pack untouched, carrying its code with it.
func stateErrorf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}
