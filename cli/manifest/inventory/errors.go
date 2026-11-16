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
	// ErrTreeScope reports --tree asked of a scope whose packages share no
	// tree.
	ErrTreeScope = errors.New("--tree applies to the project scope only")
	// ErrSwitchScope reports tt package switch asked of the project scope,
	// which holds no versions of a package side by side.
	ErrSwitchScope = errors.New("tt package switch applies to the user and system scopes only")
	// ErrVersionNotInstalled reports a switch to a version of an application
	// that is not installed.
	ErrVersionNotInstalled = errors.New("version is not installed")
	// ErrVersionCannotStart reports a switch to a version whose bundled
	// tarantool cannot be run, such as one no longer installed in the scope.
	ErrVersionCannotStart = errors.New("the version could not start")
)

// stateErrorf wraps a formatted error as a usage or state failure
// (sdk.ExitFailure): an unknown --scope, a package that is not installed, a
// refusal to remove the primary package or an application whose instances
// run, an unreadable tree, a switch to a version that is not installed or
// cannot run.
func stateErrorf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}
