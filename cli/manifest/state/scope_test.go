package state_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/state"
)

// TestParseScope covers the accepted values and the default.
func TestParseScope(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]state.Scope{
		"":        state.ScopeProject,
		"project": state.ScopeProject,
		"user":    state.ScopeUser,
		"system":  state.ScopeSystem,
	} {
		got, err := state.ParseScope(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}
}

// TestParseScopeRejectsUnknown pins the error for a value outside the closed
// set, and that the raw value is echoed back so the message is actionable.
func TestParseScopeRejectsUnknown(t *testing.T) {
	t.Parallel()

	_, err := state.ParseScope("global")
	require.Error(t, err)
	require.ErrorIs(t, err, state.ErrUnknownScope)
	assert.Contains(t, err.Error(), "global")
}

// TestAcceptsWithDeps pins the rule that only project takes a with-deps archive.
func TestAcceptsWithDeps(t *testing.T) {
	t.Parallel()

	assert.True(t, state.ScopeProject.AcceptsWithDeps())
	assert.False(t, state.ScopeUser.AcceptsWithDeps())
	assert.False(t, state.ScopeSystem.AcceptsWithDeps())
}

// TestHasPrimary pins the rule that only the project scope has a primary
// package; user and system trees hold guests only.
func TestHasPrimary(t *testing.T) {
	t.Parallel()

	assert.True(t, state.ScopeProject.HasPrimary())
	assert.False(t, state.ScopeUser.HasPrimary())
	assert.False(t, state.ScopeSystem.HasPrimary())
}

// TestResolveLayoutProject fixes the project-scope directory layout.
func TestResolveLayoutProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	lay, err := state.ResolveLayout(state.ScopeProject, dir)
	require.NoError(t, err)

	assert.Equal(t, dir, lay.Root)
	assert.Equal(t, filepath.Join(dir, ".rocks"), lay.Tree)
	assert.Equal(t, filepath.Join(dir, ".rocks", "share", "tarantool"), lay.Share)
	assert.Equal(t, filepath.Join(dir, ".rocks", "lib", "tarantool"), lay.Lib)
	assert.Equal(t, filepath.Join(dir, ".rocks", "manifests"), lay.Manifests)
}

// TestResolveLayoutUser fixes the user-scope layout: the LuaRocks 5.1
// convention under ~/.luarocks, with no .rocks/ level.
func TestResolveLayoutUser(t *testing.T) {
	t.Parallel()

	// The user scope derives its root from the environment, so the test asserts
	// the shape relative to whatever home it landed on rather than a fixed path.
	lay, err := state.ResolveLayout(state.ScopeUser, "/ignored")
	require.NoError(t, err)

	assert.Equal(t, lay.Root, lay.Tree)
	assert.Equal(t, filepath.Join(lay.Root, "share", "lua", "5.1"), lay.Share)
	assert.Equal(t, filepath.Join(lay.Root, "lib", "lua", "5.1"), lay.Lib)
	assert.Equal(t, filepath.Join(lay.Root, "manifests"), lay.Manifests)
	assert.Equal(t, ".luarocks", filepath.Base(lay.Root))
}

// TestResolveLayoutSystem fixes the system-scope layout under /usr.
func TestResolveLayoutSystem(t *testing.T) {
	t.Parallel()

	lay, err := state.ResolveLayout(state.ScopeSystem, "/ignored")
	require.NoError(t, err)

	assert.Equal(t, "/usr", lay.Root)
	assert.Equal(t, "/usr", lay.Tree)
	assert.Equal(t, filepath.Join("/usr", "share", "tarantool"), lay.Share)
	assert.Equal(t, filepath.Join("/usr", "lib", "tarantool"), lay.Lib)
	assert.Equal(t, filepath.Join("/usr", "manifests"), lay.Manifests)
}

// TestResolveLayoutRejectsUnknown pins that an unvalidated scope value cannot
// silently resolve to a directory.
func TestResolveLayoutRejectsUnknown(t *testing.T) {
	t.Parallel()

	_, err := state.ResolveLayout(state.Scope("global"), t.TempDir())
	require.Error(t, err)
	require.ErrorIs(t, err, state.ErrUnknownScope)
}
