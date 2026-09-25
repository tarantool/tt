// Package exitcode turns the error a tt command ends with into the process
// exit status, and reports that error to the user once.
//
// The codes and the carrier that sets them are the SDK's (sdk.ExitError,
// sdk.ExitCode). This package adds the policy that belongs to tt rather than
// to the contract: which uncoded failures are the system's (Code, Classify),
// which errors the user has already seen (Silent), and the one place that
// logs an error and ends the process for code that cannot return it (Exit).
package exitcode

import (
	"errors"
	"io/fs"
	"net"
	"net/url"
	"os"
	"syscall"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
)

// Code returns the process exit code for err under the manifest commands'
// contract: sdk.ExitOK for nil; a code other than sdk.ExitFailure that err
// carries; sdk.ExitSystem when err is a failure of the system (see
// systemFailure); sdk.ExitFailure otherwise.
//
// sdk.ExitFailure is the code every command wraps its errors in, so it is the
// weakest: a rock server that cannot be reached, wrapped as "resolving
// dependencies" with code 1, is still a system failure. A code other than 1 is
// a deliberate verdict - a build backend that failed, a multi-package install
// that partly succeeded - and stands.
func Code(err error) int {
	if code := sdk.ExitCode(err); code != sdk.ExitFailure {
		return code
	}

	if systemFailure(err) {
		return sdk.ExitSystem
	}

	return sdk.ExitFailure
}

// Classify returns err carrying the code Code assigns it, so that
// sdk.ExitCode, which the root exits with, reads the same number. An error
// Code leaves at the code it already carries is returned as is; nil stays nil.
//
// It is applied where a command promises the classification: the manifest
// commands. The rest of tt exits 1 for any failure that carries no code.
func Classify(err error) error {
	if err == nil {
		return nil
	}

	if code := Code(err); code != sdk.ExitCode(err) {
		return sdk.WithCode(code, err)
	}

	return err
}

// systemFailure reports whether err is a failure of the network or the machine
// rather than of the request: a server that could not be reached or did not
// answer in time, or a filesystem that refused an operation for want of
// permission or space.
//
// It matches the transport and OS error types, never *url.Error as a whole:
// the HTTP client wraps a malformed registry URL in *url.Error too, and that
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

// silentError marks an error the user has already been told about.
type silentError struct {
	err error
}

// Error renders the wrapped error.
func (e *silentError) Error() string { return e.err.Error() }

// Unwrap exposes the wrapped error to errors.Is, errors.As and sdk.ExitCode.
func (e *silentError) Unwrap() error { return e.err }

// Silent marks err as already reported, so Report does not log it: an
// external module that printed its own failure, a prompt the user declined.
// Its exit code is still err's. Silent(nil) is nil.
func Silent(err error) error {
	if err == nil {
		return nil
	}

	return &silentError{err: err}
}

// IsSilent reports whether err, or an error it wraps, was marked by Silent.
func IsSilent(err error) bool {
	var silent *silentError

	return errors.As(err, &silent)
}

// Report logs err as one error record, unless it is nil or silent.
func Report(err error) {
	if err == nil || IsSilent(err) {
		return
	}

	log.Error(err.Error())
}

// Exit reports err and ends the process with sdk.ExitCode(err); a nil err
// exits sdk.ExitOK.
//
// A command returns its error to the root, which reports it and exits there.
// Exit is for the code that cannot: a callback of an interactive prompt that
// has no caller to return to.
func Exit(err error) {
	Report(err)
	os.Exit(sdk.ExitCode(err))
}
