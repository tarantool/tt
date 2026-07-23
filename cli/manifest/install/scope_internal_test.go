package install

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseScope covers the accepted values and the default.
func TestParseScope(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]Scope{
		"":        ScopeProject,
		"project": ScopeProject,
		"user":    ScopeUser,
		"system":  ScopeSystem,
	} {
		got, err := ParseScope(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
	}

	_, err := ParseScope("global")
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnknownScope)
}

// TestAcceptsWithDeps pins the rule that only project takes a with-deps archive.
func TestAcceptsWithDeps(t *testing.T) {
	t.Parallel()

	assert.True(t, ScopeProject.AcceptsWithDeps())
	assert.False(t, ScopeUser.AcceptsWithDeps())
	assert.False(t, ScopeSystem.AcceptsWithDeps())
}

// TestResolveLayoutProject fixes the project-scope directory layout.
func TestResolveLayoutProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	lay, err := resolveLayout(ScopeProject, dir)
	require.NoError(t, err)

	assert.Equal(t, dir, lay.root)
	assert.Equal(t, filepath.Join(dir, ".rocks"), lay.tree)
	assert.Equal(t, filepath.Join(dir, ".rocks", "share", "tarantool"), lay.share)
	assert.Equal(t, filepath.Join(dir, ".rocks", "lib", "tarantool"), lay.lib)
	assert.Equal(t, filepath.Join(dir, ".rocks", "manifests"), lay.manifests)
}
