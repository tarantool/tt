package registry

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/cli/manifest/build"
)

// exitStateError is the process exit code for a usage or state failure: a
// malformed reference, a missing lock, an unusable download directory. It
// matches what build, pack, install and inventory use, so every package
// command agrees.
const exitStateError = 1

var (
	// ErrUnknownFormat reports an -o value that is not table, json or yaml.
	ErrUnknownFormat = errors.New("unknown output format")
	// ErrBadReference reports a download argument that is not NAME or
	// NAME@VERSION.
	ErrBadReference = errors.New("invalid rock reference")
	// ErrNoLock reports a download with no arguments run in a project that has
	// not resolved yet, so there is no closure to mirror.
	ErrNoLock = errors.New("no lock file")
)

// ExitError re-exports build.ExitError so this package returns the same typed
// error the other package commands do.
type ExitError = build.ExitError

// ExitCode returns the process exit code for err, reusing the build package's
// mapping so every package command agrees. A nil error is 0.
func ExitCode(err error) int {
	return build.ExitCode(err)
}

// stateErrorf wraps a formatted error as a state error (exit 1).
//
//nolint:err113 // Formatting helper, mirrors fmt.Errorf; callers pass %w wraps.
func stateErrorf(format string, args ...any) error {
	return &build.ExitError{Code: exitStateError, Err: fmt.Errorf(format, args...)}
}
