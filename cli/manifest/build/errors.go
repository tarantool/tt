package build

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"syscall"
)

// File names the build reads and writes in the project root.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
)

// Process exit codes the manifest commands map failures to: a usage or state
// error (stale lock under --locked, version.lua collision, bad manifest,
// unresolvable dependency) exits 1; a failure of the system the command ran on
// rather than of what it was asked - a component build backend that fails, a
// rock server that cannot be reached, a filesystem that refuses a write -
// exits 2.
const (
	exitStateError   = 1
	exitBackendError = 2
	exitSystemError  = 2
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

// ExitCode returns the process exit code for err. A nil error is 0. Otherwise
// it is the first code in the chain other than 1 that an ExitError carries; 2
// when the chain holds a system failure (see systemFailure); and 1 for
// anything else.
//
// Code 1 is the generic one every command wraps its errors in, so it is the
// weakest: a registry that cannot be reached, wrapped as "resolving
// dependencies" with code 1, is still a system failure and exits 2. A code
// other than 1 is a deliberate verdict - a build backend that failed, a
// multi-package install that partly succeeded - and stands.
//
// Every manifest command reaches its exit code through here, so a system
// failure is classified once, by what the error is, instead of at each of the
// many places a registry is queried or a file written.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	if code, ok := explicitCode(err); ok {
		return code
	}

	if systemFailure(err) {
		return exitSystemError
	}

	return exitStateError
}

// explicitCode returns the first code other than 1 carried by an ExitError in
// err's chain, looking past the generic code-1 wrappers to what they wrap.
func explicitCode(err error) (int, bool) {
	var exit *ExitError
	if !errors.As(err, &exit) {
		return 0, false
	}

	if exit.Code != exitStateError {
		return exit.Code, true
	}

	return explicitCode(exit.Err)
}

// systemFailure reports whether err is a failure of the network or the machine
// rather than of the request: a rock server that could not be reached or did
// not answer in time, or a filesystem that refused an operation for want of
// permission or space.
//
// It matches the transport and OS error types, never *url.Error as a whole:
// the HTTP client wraps a malformed --registry URL in *url.Error too, and that
// is the user's to fix. A missing file is not matched either - a project
// without a manifest is a usage error, not a broken machine.
func systemFailure(err error) bool {
	var (
		opErr  *net.OpError
		dnsErr *net.DNSError
		urlErr *url.Error
	)

	switch {
	case errors.As(err, &opErr), errors.As(err, &dnsErr):
		return true
	case errors.As(err, &urlErr) && urlErr.Timeout():
		return true
	case errors.Is(err, fs.ErrPermission):
		return true
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT),
		errors.Is(err, syscall.EROFS):
		return true
	default:
		return false
	}
}
