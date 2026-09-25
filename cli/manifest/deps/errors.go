package deps

import (
	"errors"

	"github.com/tarantool/tt/sdk"
)

// File names the dependency commands read and write in the project root.
const (
	manifestFileName = "app.manifest.toml"
	lockFileName     = "app.manifest.lock"
)

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

// stateErrorf wraps a formatted error as a usage or state failure
// (sdk.ExitFailure): a dependency the manifest does not declare, a declaration
// written in a form the editor refuses to rewrite, an unreadable or invalid
// manifest, a failed resolution.
func stateErrorf(format string, args ...any) error {
	return sdk.Errorf(sdk.ExitFailure, format, args...)
}
