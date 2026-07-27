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
