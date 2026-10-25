package app

import (
	"errors"

	"github.com/tarantool/tt/sdk"
)

// The failures the resolver reports. Each error it returns wraps one of
// them, so that a caller tells them apart with errors.Is rather than by the
// message, which names the paths and the names involved.
var (
	// ErrInvalidTarget is a target outside the grammar of instance-level
	// commands, such as a bare application name, or a name that cannot be
	// an application or an instance.
	ErrInvalidTarget = errors.New("invalid target")
	// ErrUnknownScope is a --scope value other than project, user and
	// system.
	ErrUnknownScope = errors.New("unknown scope")
)

// failf wraps a formatted error as a request that cannot be carried out as
// given (sdk.ExitFailure), the code of every failure here: the fix is a
// target, a flag or a file on the user's side. A failure of the system
// underneath, such as a refused read or an unreachable etcd, still ends tt
// with sdk.ExitSystem, because sdk.ExitFailure does not hide what it wraps.
func failf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}
