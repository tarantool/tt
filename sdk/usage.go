package sdk

import "fmt"

// UsageError is a command invoked the wrong way: an argument or a flag value
// the command cannot accept. tt reports it the way it reports its own
// argument errors - the message, then the command's usage - and exits
// ExitFailure unless the wrapped error carries another code.
//
// Return one from a command's RunE; test for one with errors.As.
type UsageError struct {
	// Err is the wrapped error. It must not be nil.
	Err error
}

// WithUsage marks err as a usage error. It panics when err is nil, as
// WithCode does.
func WithUsage(err error) *UsageError {
	if err == nil {
		panic("sdk.WithUsage: nil error")
	}

	return &UsageError{Err: err}
}

// Usagef formats a usage error the way fmt.Errorf does, %w included.
//
//nolint:err113 // Formatting helper, mirrors fmt.Errorf; callers pass %w wraps.
func Usagef(format string, args ...any) *UsageError {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// Error renders the wrapped error.
func (e *UsageError) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *UsageError) Unwrap() error { return e.Err }
