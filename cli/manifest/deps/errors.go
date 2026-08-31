package deps

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/cli/manifest/build"
)

// File names the dependency commands read and write in the project root.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
)

// exitStateError is the process exit code for a usage or state failure: a
// dependency the manifest does not declare, a declaration written in a form the
// editor refuses to rewrite, an unreadable or invalid manifest, a failed
// resolution. It matches what build, pack, install and inventory use, so every
// package command agrees.
const exitStateError = 1

var (
	// ErrNotDeclared reports a remove or a targeted update aimed at a name the
	// manifest's [dependencies]/[dev_dependencies] do not declare. It is
	// deliberately an error rather than a no-op: a user who mistypes a rock name
	// wants to hear about it, and "nothing to do" reads as success.
	ErrNotDeclared = errors.New("dependency is not declared in the manifest")
	// ErrManifestEdited reports a run whose manifest edit reached disk but whose
	// resolution then failed, so the lock was left as it was. It is wrapped
	// around the resolution failure rather than replacing it: the user needs
	// both why the resolve failed and the fact that the two files no longer
	// agree, because every following command will re-resolve and fail the same
	// way until the manifest is fixed or the edit undone.
	ErrManifestEdited = errors.New(
		"the manifest was edited but the lock was not updated")
)

// ExitError re-exports build.ExitError so the dependency commands return the
// same typed error the build, pack, install and inventory commands do.
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
	return &ExitError{Code: exitStateError, Err: fmt.Errorf(format, args...)}
}
