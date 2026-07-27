package install

import "github.com/tarantool/tt/cli/manifest/state"

// Scope and its values are re-exported from cli/manifest/state, which owns the
// on-disk layout install writes and list/uninstall read. The aliases keep
// install.Scope usable from the CLI wiring without every caller reaching into
// the state package for a flag value.
type Scope = state.Scope

const (
	// ScopeProject installs into <cwd>/.rocks/, the self-contained deployment
	// tree. It is the default and the only scope that accepts a with-deps
	// archive.
	ScopeProject = state.ScopeProject
	// ScopeUser installs into ~/.luarocks/, the personal LuaRocks tree.
	ScopeUser = state.ScopeUser
	// ScopeSystem installs into /usr/, the OS-package-style tree.
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
