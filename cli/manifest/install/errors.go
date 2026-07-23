package install

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/cli/manifest/build"
)

// Names install reads from inside a .tt archive. They mirror the reserved set
// tt writes at pack time.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
	versionFileName  = "VERSION"
	runtimeDirName   = "_runtime"
)

// Process exit codes install maps failures to. A usage or state error (name
// collision without --force/--upgrade, incompatible runtime, a with-deps
// archive into user/system, a stale lock under --locked, an unreconcilable
// shared dependency) exits 1. A multi-archive run where some targets installed
// and some failed exits 3.
const (
	exitStateError   = 1
	exitPartialError = 3
)

var (
	// errUnknownScope reports a --scope value that is not project, user or
	// system.
	errUnknownScope = errors.New("unknown scope")
	// errWithDepsScope reports a with-deps archive aimed at user or system,
	// which accept only --without-deps archives. Caught from the archive header,
	// before anything is written.
	errWithDepsScope = errors.New(
		"a with-deps archive can only be installed into the project scope")
	// errNameCollision reports a package already installed in the scope while
	// neither --force nor --upgrade was given.
	errNameCollision = errors.New("package already installed")
	// errLockStale reports that the archive's lock no longer matches its manifest
	// while --locked forbids proceeding.
	errLockStale = errors.New("lock is out of date")
	// errRuntimeMismatch reports a bundled runtime whose version conflicts with
	// the one the project's primary package (or an earlier install) already
	// fixed.
	errRuntimeMismatch = errors.New("bundled runtime is incompatible")
	// errIncompatibleDeps reports a shared dependency no locked version can
	// satisfy across all packages that require it.
	errIncompatibleDeps = errors.New("incompatible shared dependency")
	// errBadArchive reports a .tt archive that cannot be read as tar+zstd or that
	// is missing a tt-owned member.
	errBadArchive = errors.New("invalid archive")
	// errUnsafePath reports an archive entry whose path escapes the destination
	// directory (a tar-slip attempt).
	errUnsafePath = errors.New("archive entry escapes destination")
	// errPartialInstall reports a multi-archive run where some archives
	// installed and some failed; it carries exit code 3.
	errPartialInstall = errors.New("some archives failed to install")
)

// ExitError re-exports build.ExitError so install returns the same typed error
// the build and pack commands do; ExitCode reads the code back through the
// chain.
type ExitError = build.ExitError

// ExitCode returns the process exit code for err, reusing the build package's
// mapping so build, pack and install all agree. A nil error is 0.
func ExitCode(err error) int {
	return build.ExitCode(err)
}

// stateErrorf wraps a formatted error as a state error (exit 1).
//
//nolint:err113 // Formatting helper, mirrors fmt.Errorf; callers pass %w wraps.
func stateErrorf(format string, args ...any) error {
	return &build.ExitError{Code: exitStateError, Err: fmt.Errorf(format, args...)}
}
