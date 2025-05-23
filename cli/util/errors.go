package util

import "errors"

// ErrCmdAbort is reported when user aborts the program.
var ErrCmdAbort = errors.New("aborted by user")
