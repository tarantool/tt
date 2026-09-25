package inventory

import (
	"errors"

	"github.com/tarantool/tt/sdk"
)

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
)

// stateErrorf wraps a formatted error as a usage or state failure
// (sdk.ExitFailure): an unknown --scope, a package that is not installed, a
// refusal to remove the primary package, an unreadable tree.
func stateErrorf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}
