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
	// ErrManifestUnchanged reports a run that edits the manifest and failed
	// before the edit reached disk: the edited manifest did not parse or
	// validate, its resolution failed - an unknown rock, a constraint no
	// version satisfies, a server that did not answer, an interrupt - or
	// writing the files failed and they were put back. Both files are byte for
	// byte what they were before the command. It is wrapped around the failure
	// rather than replacing it: the user needs both why the command failed and
	// that there is nothing to undo.
	ErrManifestUnchanged = errors.New("the manifest was left unchanged")
	// ErrManifestEdited reports the one failure that leaves the two files
	// disagreeing: the edited manifest was written, writing the lock then
	// failed, and putting the manifest back failed too. Every following
	// command will re-resolve against the edited manifest until it is fixed by
	// hand, so the error says so.
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
