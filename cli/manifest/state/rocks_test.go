package state_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// inspect reads the tree of lay.
func inspect(t *testing.T, lay state.Layout) *tree.View {
	t.Helper()

	view, err := state.InspectTree(t.Context(), lay)
	require.NoError(t, err)

	return view
}

// TestInstalledVersion reads a rock's version back out of the tree, and reports
// a miss for a rock that was never installed.
func TestInstalledVersion(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeRock(t, lay, "metrics", "1.0.0")

	version, ok := state.InstalledVersion(inspect(t, lay), "metrics")
	require.True(t, ok)
	assert.Equal(t, "1.0.0-1", version)

	_, ok = state.InstalledVersion(inspect(t, lay), "absent")
	assert.False(t, ok)

	_, ok = state.InstalledVersion(nil, "metrics")
	assert.False(t, ok, "a tree that could not be read holds nothing")
}

// TestInstalledVersionIgnoresStrayFiles pins that only a rock the tree
// manifest lists counts as installed: a directory under rocks/<name>/ that no
// install recorded must not be read as one.
func TestInstalledVersionIgnoresStrayFiles(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeFile(t, filepath.Join(lay.Share, "rocks", "metrics", "README"), []byte("junk\n"))
	writeFile(t, filepath.Join(lay.Share, "rocks", "metrics", "1.0.0-1", "rock_manifest"),
		[]byte("rock_manifest = {}\n"))

	_, ok := state.InstalledVersion(inspect(t, lay), "metrics")
	assert.False(t, ok)
}

// TestInstalledVersionPrefersTheNewest pins which version answers for a rock
// the tree holds twice.
func TestInstalledVersionPrefersTheNewest(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeRock(t, lay, "metrics", "1.0.0")
	writeRock(t, lay, "metrics", "2.0.0")

	version, ok := state.InstalledVersion(inspect(t, lay), "metrics")
	require.True(t, ok)
	assert.Equal(t, "2.0.0-1", version)
}

// TestRemoveUntrackedCoversEveryTree pins that removing a rock the tree
// manifest does not list clears it from share/, lib/ and the rocks dir at once,
// and prunes the rock's directory there once it is empty.
func TestRemoveUntrackedCoversEveryTree(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeFile(t, filepath.Join(lay.Share, "metrics", "init.lua"), []byte("-- metrics\n"))
	writeFile(t, filepath.Join(lay.Lib, "metrics", "metrics.so"), []byte("binary\n"))
	writeFile(t, filepath.Join(lay.Share, "rocks", "metrics", "1.0.0-1", "rock_manifest"),
		[]byte("rock_manifest = {}\n"))

	require.NoError(t, state.RemoveUntracked(lay, "metrics", "1.0.0-1"))

	assert.NoDirExists(t, filepath.Join(lay.Share, "metrics"))
	assert.NoDirExists(t, filepath.Join(lay.Lib, "metrics"))
	assert.NoDirExists(t, state.RockRoot(lay, "metrics"))
}

// TestSameVersion covers the tolerance the reconciler and uninstall both need:
// a LuaRocks revision suffix is not a different version.
func TestSameVersion(t *testing.T) {
	t.Parallel()

	assert.True(t, state.SameVersion("1.0.0", "1.0.0"))
	assert.True(t, state.SameVersion("1.0.0", "1.0.0-1"))
	assert.False(t, state.SameVersion("1.0.0", "1.0.1"))
	assert.False(t, state.SameVersion("nonsense", "1.0.0"))
	assert.False(t, state.SameVersion("1.0.0", "nonsense"))
	assert.True(t, state.SameVersion("nonsense", "nonsense"))
}

// writeTreeManifestFixture lays a LuaRocks tree manifest by hand, in the bare
// assignment format LuaRocks actually writes.
func writeTreeManifestFixture(t *testing.T, lay state.Layout, body string) {
	t.Helper()

	writeFile(t, filepath.Join(lay.Share, "rocks", "manifest"), []byte(body))
}

// TestDependencyEdges reads the rock-to-rock edges out of the tree manifest.
// These are what the lock throws away: it stores the closure flattened, so the
// tree is the only place the parent of an indirect rock survives.
func TestDependencyEdges(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeRock(t, lay, "metrics", "1.0.0", "luasocket", "checks >= 3.0")
	writeRock(t, lay, "shared-lib", "1.0.0", "luasocket")
	writeRock(t, lay, "checks", "3.1.0")

	edges := state.DependencyEdges(inspect(t, lay))

	assert.Equal(t, []string{"checks", "luasocket"}, edges["metrics"],
		"a rock's requirements are deduplicated and in name order")
	assert.Equal(t, []string{"luasocket"}, edges["shared-lib"])
	assert.NotContains(t, edges, "checks", "a rock that requires nothing has no entry")
}

// TestDependencyEdgesMergesVersions pins that edges are unioned across the
// versions of a rock the tree happens to record.
func TestDependencyEdgesMergesVersions(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeRock(t, lay, "metrics", "1.0.0", "checks")
	writeRock(t, lay, "metrics", "2.0.0", "luasocket")

	assert.Equal(t, []string{"checks", "luasocket"},
		state.DependencyEdges(inspect(t, lay))["metrics"])
}

// TestDependencyEdgesDropsSelfReference pins the guard that keeps a graph walk
// from cycling on its first step.
func TestDependencyEdgesDropsSelfReference(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeRock(t, lay, "metrics", "1.0.0", "metrics", "checks")

	assert.Equal(t, []string{"checks"}, state.DependencyEdges(inspect(t, lay))["metrics"])
}

// TestDependencyEdgesAbsentTree pins the graceful path: the edges are an
// enrichment, so a tree with no manifest yields nothing, and one whose
// manifest does not parse fails to be read rather than being half-read.
func TestDependencyEdgesAbsentTree(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	assert.Empty(t, state.DependencyEdges(inspect(t, lay)), "no manifest at all")
	assert.Nil(t, state.DependencyEdges(nil), "no tree read")

	writeTreeManifestFixture(t, lay, "this is not a lua table")

	_, err := state.InspectTree(t.Context(), lay)
	require.ErrorIs(t, err, tree.ErrCorrupt)
}
