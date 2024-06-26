package util

import "errors"

var (
	// ErrCmdAbort is reported when user aborts the program.
	ErrCmdAbort = errors.New("aborted by user")
)
