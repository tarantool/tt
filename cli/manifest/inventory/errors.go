package inventory

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/cli/manifest/build"
)

// exitStateError is the process exit code for a usage or state failure: an
// unknown --scope, a package that is not installed, a refusal to remove the
// primary package, an unreadable tree. It matches what build, pack and install
// use, so every package command agrees.
const exitStateError = 1

var (
	// ErrNotInstalled reports an uninstall aimed at a package the scope does not
	// hold.
	ErrNotInstalled = errors.New("package is not installed")
	// ErrPrimaryPackage reports an uninstall aimed at the project's own package.
	// Its place in the tree is the project's to decide, not a guest's, so
	// uninstall refuses rather than gutting the working directory.
	ErrPrimaryPackage = errors.New("cannot uninstall the project's primary package")
	// ErrBadPackageName reports an argument that is not a well-formed package
	// name. Uninstall turns the name into a path, so the shape is checked before
	// anything is removed.
	ErrBadPackageName = errors.New("invalid package name")
	// ErrAborted reports that the user declined the confirmation prompt; nothing
	// was removed.
	ErrAborted = errors.New("aborted")
	// ErrUnknownFormat reports an -o value that is not table, json or yaml.
	ErrUnknownFormat = errors.New("unknown output format")
)

// ExitError re-exports build.ExitError so inventory returns the same typed
// error the build, pack and install commands do.
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
