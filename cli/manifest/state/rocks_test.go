package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest/state"
)

// TestInstalledVersion reads a rock's version back out of the tree, and reports
// a miss for a rock that was never installed.
func TestInstalledVersion(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeRock(t, lay, "metrics", "1.0.0")

	version, ok := state.InstalledVersion(lay, "metrics")
	require.True(t, ok)
	assert.Equal(t, "1.0.0", version)

	_, ok = state.InstalledVersion(lay, "absent")
	assert.False(t, ok)
}

// TestInstalledVersionIgnoresStrayFiles pins that only a directory counts as a
// version: a stray file under rocks/<name>/ must not be read as one.
func TestInstalledVersionIgnoresStrayFiles(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeFile(t, filepath.Join(lay.Share, "rocks", "metrics", "README"), []byte("junk\n"))

	_, ok := state.InstalledVersion(lay, "metrics")
	assert.False(t, ok)
}

// TestRockPathsCoverEveryTree pins that removing a rock's paths clears it from
// share/, lib/ and the rock-manifest tree at once — the invariant uninstall's
// dependency removal depends on.
func TestRockPathsCoverEveryTree(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeRock(t, lay, "metrics", "1.0.0")

	for _, path := range state.RockPaths(lay, "metrics", "1.0.0") {
		require.NoError(t, os.RemoveAll(path))
	}

	assert.NoDirExists(t, filepath.Join(lay.Share, "metrics"))
	assert.NoDirExists(t, filepath.Join(lay.Lib, "metrics"))
	assert.NoDirExists(t, filepath.Join(lay.Share, "rocks", "metrics", "1.0.0"))

	_, ok := state.InstalledVersion(lay, "metrics")
	assert.False(t, ok)
}

// TestRockRoot points at the per-rock manifest directory across versions.
func TestRockRoot(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	assert.Equal(t,
		filepath.Join(lay.Share, "rocks", "metrics"),
		state.RockRoot(lay, "metrics"))
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
// assignment format LuaRocks actually writes — not a "return {...}" table, and
// with every key bracket-quoted so a hyphenated rock name stays valid Lua.
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

	writeTreeManifestFixture(t, lay, `commands = {}
modules = {}
repository = {}
dependencies = {
   ["metrics"] = {
      ["1.0.0"] = {
         { name = "checks", constraints = {} },
         { name = "luasocket", constraints = {} },
      }
   },
   ["shared-lib"] = {
      ["1.0.0"] = {
         { name = "luasocket", constraints = {} },
      }
   },
}
`)

	edges := state.DependencyEdges(lay)

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

	writeTreeManifestFixture(t, lay, `dependencies = {
   ["metrics"] = {
      ["1.0.0"] = {
         { name = "checks", constraints = {} },
      },
      ["2.0.0"] = {
         { name = "luasocket", constraints = {} },
      }
   },
}
`)

	assert.Equal(t, []string{"checks", "luasocket"}, state.DependencyEdges(lay)["metrics"])
}

// TestDependencyEdgesDropsSelfReference pins the guard that keeps a graph walk
// from cycling on its first step.
func TestDependencyEdgesDropsSelfReference(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	writeTreeManifestFixture(t, lay, `dependencies = {
   ["metrics"] = {
      ["1.0.0"] = {
         { name = "metrics", constraints = {} },
         { name = "checks", constraints = {} },
      }
   },
}
`)

	assert.Equal(t, []string{"checks"}, state.DependencyEdges(lay)["metrics"])
}

// TestDependencyEdgesAbsentTree pins the graceful path: the edges are an
// enrichment, so a tree with no manifest — or an unparseable one — yields
// nothing rather than failing the whole listing.
func TestDependencyEdgesAbsentTree(t *testing.T) {
	t.Parallel()

	lay := projectLayout(t)

	assert.Nil(t, state.DependencyEdges(lay), "no manifest at all")

	writeTreeManifestFixture(t, lay, "this is not a lua table")
	assert.Nil(t, state.DependencyEdges(lay), "an unparseable manifest")

	// A "return {...}" wrapper is not the format LuaRocks writes, and must not be
	// silently half-read.
	writeTreeManifestFixture(t, lay, "return { dependencies = {} }")
	assert.Nil(t, state.DependencyEdges(lay))
}
