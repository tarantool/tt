package sdk

import (
	"errors"
	"fmt"
)

// Process exit codes tt returns. `tt help errors` describes them to users.
const (
	// ExitOK is a command that did what it was asked.
	ExitOK = 0
	// ExitFailure is a request that cannot be carried out as given, and the
	// fix is on the caller's side: an invalid manifest, a stale lock, an
	// unresolvable dependency, a malformed URL, a missing path. It is also the
	// code of every error that carries none.
	ExitFailure = 1
	// ExitSystem is a failure of the system the command ran on rather than of
	// the request: a build backend that failed, a server that could not be
	// reached or did not answer in time, a filesystem that refused an
	// operation for want of permission or space.
	ExitSystem = 2
	// ExitPartial is a multi-target operation that partly succeeded: some of
	// its targets went through and some did not.
	ExitPartial = 3
)

// ExitCoder is implemented by errors that carry the process exit code tt
// should return for them.
//
// The method is called ExitStatus rather than ExitCode on purpose:
// *exec.ExitError has an ExitCode method that reports a child process's
// status (-1 when it was killed by a signal), and a child's status wrapped
// deep in an error chain is not tt's verdict on the command.
type ExitCoder interface {
	error
	// ExitStatus returns the process exit code for this error.
	ExitStatus() int
}

// ExitError wraps an error with the process exit code tt should return for it.
type ExitError struct {
	// Code is the process exit code, one of the Exit* constants.
	Code int
	// Err is the wrapped error. It must not be nil.
	Err error
}

// WithCode wraps err with an exit code. It panics when err is nil: an exit
// code without an error would turn a success into a failure.
func WithCode(code int, err error) *ExitError {
	if err == nil {
		panic("sdk.WithCode: nil error")
	}

	return &ExitError{Code: code, Err: err}
}

// Errorf formats an error the way fmt.Errorf does, %w included, and wraps it
// with an exit code.
//
//nolint:err113 // Formatting helper, mirrors fmt.Errorf; callers pass %w wraps.
func Errorf(code int, format string, args ...any) *ExitError {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// Error renders the wrapped error.
func (e *ExitError) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitStatus returns Code.
func (e *ExitError) ExitStatus() int { return e.Code }

// reportedError is an error the user has already been told about.
type reportedError struct {
	err error
}

// Error renders the wrapped error.
func (e *reportedError) Error() string { return e.err.Error() }

// Unwrap exposes the wrapped error to errors.Is, errors.As and ExitCode.
func (e *reportedError) Unwrap() error { return e.err }

// Reported marks err as one the user has already seen, so that tt does not
// report it again: the failure a child program printed itself, a prompt the
// user declined. tt logs nothing for it, and prints no usage even when it is
// a [UsageError]; the process still exits with err's code ([ExitCode]), and
// errors.Is and errors.As see through the mark. Wrapping the mark keeps it,
// and so does wrapping it in a code: Reported(WithCode(3, err)) and
// WithCode(3, Reported(err)) both exit 3 and print nothing. Reported(nil) is
// nil.
func Reported(err error) error {
	if err == nil {
		return nil
	}

	return &reportedError{err: err}
}

// IsReported reports whether err, or an error it wraps, was marked by
// [Reported].
func IsReported(err error) bool {
	var reported *reportedError

	return errors.As(err, &reported)
}

// ExitCode returns the process exit code for err: ExitOK for nil, otherwise
// the first code other than ExitFailure that an ExitCoder in the chain
// carries, and ExitFailure when there is none.
//
// ExitFailure is the weakest code. It is the one a command wraps its errors
// in generically, so it does not hide what it wraps: a build backend failure
// (ExitSystem) wrapped as "building the package" with ExitFailure still exits
// ExitSystem. Any other code is a deliberate verdict and stands, even over a
// different code deeper in the chain.
//
// The search follows errors.As order. After a code-1 carrier it continues
// into what that carrier wraps, not into its siblings in a joined error.
//
// Classifying errors that carry no code - a failed dial, a refused write -
// as ExitSystem is not done here: that policy is tt's, applied once to every
// command's error, and a result of ExitFailure from this function is its
// input.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}

	if code, ok := explicitCode(err); ok {
		return code
	}

	return ExitFailure
}

// explicitCode returns the first code other than ExitFailure carried by an
// ExitCoder in err's chain, looking past code-1 carriers to what they wrap.
func explicitCode(err error) (int, bool) {
	var coder ExitCoder

	if !errors.As(err, &coder) {
		return 0, false
	}

	code := coder.ExitStatus()
	if code != ExitFailure {
		return code, true
	}

	switch wrapper := coder.(type) {
	case interface{ Unwrap() error }:
		return explicitCode(wrapper.Unwrap())
	case interface{ Unwrap() []error }:
		for _, inner := range wrapper.Unwrap() {
			code, ok := explicitCode(inner)
			if ok {
				return code, true
			}
		}
	}

	return 0, false
}
