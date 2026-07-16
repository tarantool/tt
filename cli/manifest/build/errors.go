package build

import (
	"errors"
	"fmt"
)

// File names the build reads and writes in the project root.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
)

// Process exit codes the build maps failures to: a usage or state error (stale
// lock under --locked, version.lua collision, bad manifest) exits 1; a
// component build backend that fails exits 2.
const (
	exitStateError   = 1
	exitBackendError = 2
)

var (
	// errLockStale reports that the lock no longer matches the manifest while
	// --locked forbids rewriting it. It carries exit code 1.
	errLockStale = errors.New("lock is out of date")
	// errNoLock reports that an operation requiring a lock found none:
	// tt package fetch (which never resolves) or a --locked build.
	errNoLock = errors.New("no lock file")
	// errVersionLuaCollision reports that a component laid a file at the path
	// the build generates version.lua into. It carries exit code 1.
	errVersionLuaCollision = errors.New("version.lua collision")
	// errUnknownProduct reports that --product named a product the manifest
	// does not define.
	errUnknownProduct = errors.New("unknown product")
	// errNoDefaultProduct reports that no product could be selected: several
	// products exist and none is marked default (should be caught by Validate,
	// but the build fails safe).
	errNoDefaultProduct = errors.New("no default product")
	// errUnknownComponent reports that a component argument names a component
	// the selected product does not build.
	errUnknownComponent = errors.New("unknown component")
	// errNoProducts reports that the manifest defines no products at all.
	errNoProducts = errors.New("manifest defines no products")
	// errUnknownSource reports a lock dependency whose source is neither
	// registry nor path.
	errUnknownSource = errors.New("unknown dependency source")
	// errAmbiguousRockspec reports a path dependency directory that ships more
	// than one rockspec, so which one to build is ambiguous.
	errAmbiguousRockspec = errors.New("multiple rockspecs in path dependency")
)

// ExitError wraps an error with the process exit code the CLI should return.
// The build produces it for the two codes above; ExitCode reads it back.
type ExitError struct {
	Code int
	Err  error
}

// Error renders the wrapped error.
func (e *ExitError) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped error to errors.Is / errors.As.
func (e *ExitError) Unwrap() error { return e.Err }

// exitErrorf wraps a formatted error with an exit code. It is the exit-code
// analogue of fmt.Errorf, so the format string carries the message (and any %w
// wrap) rather than a static sentinel.
//
//nolint:err113 // Formatting helper, mirrors fmt.Errorf; callers pass %w wraps.
func exitErrorf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// ExitCode returns the process exit code for err: the code carried by an
// ExitError in its chain, or 1 for any other non-nil error. A nil error is 0.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}

	return exitStateError
}
