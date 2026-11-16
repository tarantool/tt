package state_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg/buildregtest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
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

	writeFile(t, filepath.Join(state.BinDir(lay), "metrics-cli"), []byte("binary\n"))
	writeFile(t, filepath.Join(state.BinDir(lay), "other"), []byte("binary\n"))

	require.NoError(t, state.RemoveUntracked(lay, "metrics", "1.0.0-1", []string{"metrics-cli"}))

	assert.NoDirExists(t, filepath.Join(lay.Share, "metrics"))
	assert.NoDirExists(t, filepath.Join(lay.Lib, "metrics"))
	assert.NoDirExists(t, state.RockRoot(lay, "metrics"))
	assert.NoFileExists(t, filepath.Join(state.BinDir(lay), "metrics-cli"))
	assert.FileExists(t, filepath.Join(state.BinDir(lay), "other"),
		"a program of bin/ the caller does not name stays")
}

// TestRemoveUntrackedRefusesAPathForAProgram pins that a program is one file
// of bin/: a name that leaves it removes nothing.
func TestRemoveUntrackedRefusesAPathForAProgram(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)
	outside := filepath.Join(lay.Root, "keep")

	writeFile(t, outside, []byte("keep\n"))

	for _, program := range []string{"", "..", "../../keep", "sub/tool"} {
		require.Error(t, state.RemoveUntracked(lay, "metrics", "1.0.0-1", []string{program}),
			program)
	}

	assert.FileExists(t, outside)
}

// legacyGuestManifest is the manifest of the guest monitoring, whose
// components make the programs mon-tool, by the output of a shell build,
// and agent, of kind = "executable".
const legacyGuestManifest = `manifest_version = '0.1'

[package]
name = 'monitoring'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.tool]
path = 'tool'

[components.tool.build]
backend = 'shell'
command = 'sh'
output = [{ path = 'build/mon-tool', to = 'bin' }]

[components.agent]
path = 'agent'
kind = 'executable'

[components.agent.build]
backend = 'exe'
`

// TestLegacyProgramsAreTheGuestsUnownedPrograms covers a guest an older tt
// installed: of the files of bin/, its programs are those its manifest makes
// that no rock owns; a program of another rock, and a file nothing makes,
// are not its.
func TestLegacyProgramsAreTheGuestsUnownedPrograms(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	treetest.Install(t, lay.Tree, treetest.Rock{Name: "luatest", Version: "1.0.1-1",
		Files: map[string]string{
			"lua/luatest/init.lua": "-- luatest\n",
			"bin/mon-tool":         "#!/bin/sh\n",
		}})

	view := inspect(t, lay)
	_, _, owned := view.Owner("bin/mon-tool")
	require.True(t, owned, "the fixture has a rock own a program of the guest's name")

	man, _, err := manifest.ParseManifest([]byte(legacyGuestManifest))
	require.NoError(t, err)

	programs, err := state.LegacyPrograms(lay, view, man, buildregtest.Registry())
	require.NoError(t, err)
	assert.Empty(t, programs, "a program a rock owns is the rock's")

	treetest.Remove(t, lay.Tree, "luatest", "1.0.1-1")

	for _, name := range []string{"mon-tool", "agent", "stranger"} {
		writeFile(t, filepath.Join(state.BinDir(lay), name), []byte("binary\n"))
	}

	programs, err = state.LegacyPrograms(lay, inspect(t, lay), man, buildregtest.Registry())
	require.NoError(t, err)
	assert.Equal(t, []string{"agent", "mon-tool"}, programs)

	treetest.Install(t, lay.Tree, treetest.Rock{Name: "monitoring", Version: "1.0.0-1"})

	programs, err = state.LegacyPrograms(lay, inspect(t, lay), man, buildregtest.Registry())
	require.NoError(t, err)
	assert.Empty(t, programs, "a guest that is a rock has its programs in its rock_manifest")

	programs, err = state.LegacyPrograms(lay, inspect(t, lay), nil, buildregtest.Registry())
	require.NoError(t, err)
	assert.Empty(t, programs)
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
