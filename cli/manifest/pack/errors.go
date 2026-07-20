package pack

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/cli/manifest/build"
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

// exitStateError is the exit code for a usage or state error, matching tt
// package build so the two commands are indistinguishable to a CI script.
//
// Pack defines no code of its own beyond this one: a build backend failure
// exits 2, but that error is raised inside cli/manifest/build and passes
// through pack untouched, carrying its code with it.
const exitStateError = 1

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
	// errNoTarantoolLicense reports a bundled Tarantool whose LICENSE file is
	// missing; shipping the binary without it is not permitted.
	errNoTarantoolLicense = errors.New("bundled Tarantool has no LICENSE")
	// errMissingInclude reports an include or license_files entry that matched
	// nothing on disk.
	errMissingInclude = errors.New("no such file")
	// errEscapingPath reports an include or license_files entry that resolves
	// outside the project directory.
	errEscapingPath = errors.New("path escapes the project directory")
)

// ExitCode returns the process exit code for err, reusing the build package's
// mapping so pack and build agree: a build run driven by pack surfaces its own
// exit codes unchanged. A nil error is 0.
func ExitCode(err error) int {
	return build.ExitCode(err)
}

// stateErrorf wraps a formatted error as a state error (exit 1), using the
// build package's ExitError so build.ExitCode reads it back through pack's
// chain.
//
//nolint:err113 // Formatting helper, mirrors fmt.Errorf; callers pass %w wraps.
func stateErrorf(format string, args ...any) error {
	return &build.ExitError{Code: exitStateError, Err: fmt.Errorf(format, args...)}
}
