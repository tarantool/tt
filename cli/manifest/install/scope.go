package install

import "github.com/tarantool/tt/v3/cli/manifest/state"

// Scope and its values are re-exported from cli/manifest/state, which owns the
// on-disk layout install writes and list/uninstall read. The aliases keep
// install.Scope usable from the CLI wiring without every caller reaching into
// the state package for a flag value.
type Scope = state.Scope

const (
	// ScopeProject installs into the project directory: the package joins
	// its shared .rocks/ tree as a guest. It is the default.
	ScopeProject = state.ScopeProject
	// ScopeUser installs an application version into the user root,
	// <root>/versions/<app>/<app>_<ver>, next to what tt binary installs
	// for the user.
	ScopeUser = state.ScopeUser
	// ScopeSystem installs an application version into the system root the
	// same way.
	ScopeSystem = state.ScopeSystem
)

// ParseScope validates a --scope value, defaulting an empty string to project.
// A bad value is a state error, so a mistyped --scope exits 1 like every other
// usage failure rather than escaping as an untyped error.
func ParseScope(raw string) (Scope, error) {
	scope, err := state.ParseScope(raw)
	if err != nil {
		return "", stateErrorf("%w", err)
	}

	return scope, nil
}
