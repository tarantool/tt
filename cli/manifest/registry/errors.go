package registry

import (
	"errors"

	"github.com/tarantool/tt/sdk"
)

var (
	// ErrBadReference reports a download argument that is not NAME or
	// NAME@VERSION.
	ErrBadReference = errors.New("invalid rock reference")
	// ErrNoLock reports a download with no arguments run in a project that has
	// not resolved yet, so there is no closure to mirror.
	ErrNoLock = errors.New("no lock file")
)

// stateErrorf wraps a formatted error as a usage or state failure
// (sdk.ExitFailure): a malformed reference, a missing lock, an unusable
// download directory.
func stateErrorf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}
