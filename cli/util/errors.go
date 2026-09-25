package util

import (
	"errors"

	"github.com/tarantool/tt/v3/cli/exitcode"
)

// errAbortedByUser is the cause carried by ErrCmdAbort.
var errAbortedByUser = errors.New("aborted by user")

// ErrCmdAbort is reported when user aborts the program. It is silent: the
// user who declined knows, so tt exits 1 without printing it.
var ErrCmdAbort = exitcode.Silent(errAbortedByUser)
