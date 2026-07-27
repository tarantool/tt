package inventory_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/inventory"
	"github.com/tarantool/tt/cli/manifest/state"
)

// userTree points a fixture tree at a fake home directory, so the user scope
// resolves into a temp dir instead of the developer's real ~/.luarocks.
//
// These tests cannot run in parallel: t.Setenv is process-wide.
func userTree(t *testing.T) tree {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)

	lay, err := state.ResolveLayout(state.ScopeUser, "")
	require.NoError(t, err)

	require.Equal(t, filepath.Join(home, ".luarocks"), lay.Root,
		"the user scope must resolve under the overridden home")

	return tree{dir: home, lay: lay}
}

// TestUninstallUserScope is the task's user-scope case: the removal happens in
// the user tree, addressed through --scope rather than the project directory.
func TestUninstallUserScope(t *testing.T) {
	tr := userTree(t)

	tr.installGuest(t, pkg{
		name: "some-lib", version: "2.0.0",
		pins: map[string]string{"metrics": "1.0.0"},
	})

	result, err := inventory.Uninstall(inventory.UninstallOptions{
		Scope: state.ScopeUser, Package: "some-lib", Yes: true,
	})
	require.NoError(t, err)

	assert.Equal(t, state.ScopeUser, result.Scope)
	assert.Equal(t, []string{"metrics"}, result.RemovedDependencies)

	assert.False(t, tr.rockExists("some-lib"))
	assert.False(t, tr.metadataExists("some-lib"))
	assert.False(t, tr.rockExists("metrics"))
}

// TestListUserScope covers the read side of the same tree, including that a
// stray app.manifest.toml at the root of a shared tree is not read as a primary
// package — user trees hold guests only.
func TestListUserScope(t *testing.T) {
	tr := userTree(t)

	tr.installGuest(t, pkg{name: "some-lib", version: "2.0.0"})
	tr.write(t, filepath.Join(tr.lay.Root, state.PrimaryManifestFile),
		manifestTOML(pkg{name: "not-a-primary", version: "9.9.9"}))

	listing, err := inventory.List(inventory.ListOptions{Scope: state.ScopeUser})
	require.NoError(t, err)

	assert.Equal(t, state.ScopeUser, listing.Scope)
	require.Len(t, listing.Packages, 1)
	assert.Equal(t, "some-lib", listing.Packages[0].Name)
	assert.False(t, listing.Packages[0].Primary)
}

// TestUninstallUserScopeIgnoresProjectDir pins that the user scope is addressed
// by scope alone: a ProjectDir pointing at an unrelated tree must not redirect
// the removal.
func TestUninstallUserScopeIgnoresProjectDir(t *testing.T) {
	tr := userTree(t)

	tr.installGuest(t, pkg{name: "some-lib", version: "2.0.0"})

	// A project tree holding a same-named guest, which must be left alone.
	project := newTree(t)
	project.installGuest(t, pkg{name: "some-lib", version: "2.0.0"})

	_, err := inventory.Uninstall(inventory.UninstallOptions{
		ProjectDir: project.dir, Scope: state.ScopeUser, Package: "some-lib", Yes: true,
	})
	require.NoError(t, err)

	assert.False(t, tr.metadataExists("some-lib"), "the user tree's copy is removed")
	assert.True(t, project.metadataExists("some-lib"), "the project tree is untouched")
}
