package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// pruneLock is a lock pinning checks 3.1.0-1 in the default product and
// luatest 1.0.1-1 in the dev closure.
func pruneLock() *manifest.Lock {
	return &manifest.Lock{
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				registryDep("checks", "3.1.0-1"),
			}},
		},
		DevDependencies: []manifest.LockDependency{registryDep("luatest", "1.0.1-1")},
	}
}

// prune runs pruneTree over the project at projectDir and returns the lines
// it reported.
func prune(t *testing.T, projectDir string, lock *manifest.Lock,
	extra ...manifest.LockDependency,
) []string {
	t.Helper()

	var logged []string

	cfg := luarocks.Config{Tree: filepath.Join(projectDir, rocksDirName), WorkingDir: projectDir}

	require.NoError(t, pruneTree(t.Context(), cfg, projectDir, lock, extra,
		func(rock prunedRock) { logged = append(logged, rock.String()) }))

	return logged
}

// lockOf is a lock pinning deps in the default product.
func lockOf(deps []manifest.LockDependency) *manifest.Lock {
	return &manifest.Lock{
		Products: map[string]manifest.LockProduct{"default": {Dependencies: deps}},
	}
}

// TestPruneRemovesWhatTheLockDropped covers tt package remove, then a build:
// the rock the lock held at the last materialization and holds no more goes,
// with every file its rock_manifest lists and its entry of the tree manifest,
// and a rock the lock never held stays.
func TestPruneRemovesWhatTheLockDropped(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	lay := state.ProjectLayout(projectDir)

	treetest.Install(t, lay.Tree,
		treetest.Rock{Name: "checks", Version: "3.1.0-1"},
		treetest.Rock{Name: "metrics", Version: "1.0.0-1", Files: map[string]string{
			"lua/metrics/init.lua":    "-- metrics\n",
			"lua/metrics/version.lua": "return '1.0.0'\n",
			"lua/metrics.lua":         "return require('metrics.init')\n",
			"lib/metrics/metrics.so":  "binary\n",
			"bin/metrics-cli":         "binary\n",
		}},
		treetest.Rock{Name: "luatest", Version: "1.0.1-1"})

	both := []manifest.LockDependency{
		registryDep("checks", "3.1.0-1"), registryDep("metrics", "1.0.0-1"),
	}

	assert.Empty(t, prune(t, projectDir, lockOf(both)))

	recorded, ok, err := state.ReadMaterialized(lay)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, map[string]bool{"checks": true, "metrics": true}, recorded)

	logged := prune(t, projectDir, lockOf(both[:1]))

	assert.Equal(t, []string{"removed unused dependency metrics 1.0.0-1"}, logged)
	assert.Empty(t, treetest.Versions(t, lay.Tree, "metrics"))
	assert.NoFileExists(t, filepath.Join(lay.Share, "metrics.lua"))
	assert.NoDirExists(t, filepath.Join(lay.Share, "metrics"))
	assert.NoDirExists(t, filepath.Join(lay.Lib, "metrics"))
	assert.NoDirExists(t, state.RockRoot(lay, "metrics"))
	assert.NoFileExists(t, filepath.Join(state.BinDir(lay), "metrics-cli"),
		"a program of the rock goes with it")

	manifestText, err := os.ReadFile(filepath.Join(lay.Share, "rocks", "manifest"))
	require.NoError(t, err)
	assert.NotContains(t, string(manifestText), "metrics")

	assert.Equal(t, []string{"3.1.0-1"}, treetest.Versions(t, lay.Tree, "checks"))
	assert.Equal(t, []string{"1.0.1-1"}, treetest.Versions(t, lay.Tree, "luatest"),
		"a rock the lock never held is not the lock's to remove")

	recorded, _, err = state.ReadMaterialized(lay)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"checks": true}, recorded)

	assert.Empty(t, prune(t, projectDir, lockOf(both[:1])), "nothing is left to prune")
}

// TestPruneRemovesADroppedDevDependency covers the dev closure: a build
// materializes it, so it is pinned while the lock holds it, and pruned once
// the lock drops it.
func TestPruneRemovesADroppedDevDependency(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	root := filepath.Join(projectDir, rocksDirName)

	treetest.Install(t, root,
		treetest.Rock{Name: "checks", Version: "3.1.0-1"},
		treetest.Rock{Name: "luatest", Version: "1.0.1-1"})

	assert.Empty(t, prune(t, projectDir, pruneLock()))
	assert.Equal(t, []string{"1.0.1-1"}, treetest.Versions(t, root, "luatest"))

	logged := prune(t, projectDir, lockOf([]manifest.LockDependency{
		registryDep("checks", "3.1.0-1"),
	}))

	assert.Equal(t, []string{"removed unused dependency luatest 1.0.1-1"}, logged)
	assert.Empty(t, treetest.Versions(t, root, "luatest"))
}

// TestPruneKeepsADroppedRockAGuestHolds covers a guest package of the
// project: the rocks its lock pins are its own, as tt package uninstall
// counts them, whatever the lock of the project dropped. The project's own
// rock is kept too.
func TestPruneKeepsADroppedRockAGuestHolds(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	lay := state.ProjectLayout(projectDir)

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, state.PrimaryManifestFile),
		[]byte("manifest_version = '0.1'\n[package]\nname = 'my-app'\n"), 0o600))

	guestLock := manifest.Lock{
		LockVersion: manifest.LockVersion, ManifestVersion: manifest.ManifestVersion,
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{registryDep("metrics", "1.0.0-1")}},
		},
	}
	guestLockBytes, err := guestLock.Marshal()
	require.NoError(t, err)
	require.NoError(t, state.WriteMetadata(lay, "monitoring",
		[]byte("manifest_version = '0.1'\n[package]\nname = 'monitoring'\n"),
		guestLockBytes, "2.0.0"))

	treetest.Install(t, lay.Tree,
		treetest.Rock{Name: "my-app", Version: "0.1.0-1"},
		treetest.Rock{Name: "monitoring", Version: "2.0.0-1"},
		treetest.Rock{Name: "metrics", Version: "1.0.0-1"},
		treetest.Rock{Name: "checks", Version: "3.1.0-1"})

	both := []manifest.LockDependency{
		registryDep("checks", "3.1.0-1"), registryDep("metrics", "1.0.0-1"),
	}

	assert.Empty(t, prune(t, projectDir, lockOf(both)))

	logged := prune(t, projectDir, lockOf(nil))

	assert.Equal(t, []string{"removed unused dependency checks 3.1.0-1"}, logged)

	for _, name := range []string{"my-app", "monitoring", "metrics"} {
		assert.Len(t, treetest.Versions(t, lay.Tree, name), 1, name)
	}

	packages, err := state.Packages(lay)
	require.NoError(t, err)
	assert.Len(t, packages, 2, "the record is no guest")
}

// TestPruneRecordsTheLockAlone covers the rocks a caller pins beside the lock,
// the luatest tt test resolves for a project that declares none: they are
// kept, but not recorded as the lock's, so the next build, which pins none of
// them, keeps them too.
func TestPruneRecordsTheLockAlone(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	root := filepath.Join(projectDir, rocksDirName)
	lock := lockOf([]manifest.LockDependency{registryDep("checks", "3.1.0-1")})

	treetest.Install(t, root,
		treetest.Rock{Name: "checks", Version: "3.1.0-1"},
		treetest.Rock{Name: "luatest", Version: "1.0.1-1"})

	assert.Empty(t, prune(t, projectDir, lock, registryDep("luatest", "1.0.1-1")))
	assert.Empty(t, prune(t, projectDir, lock))
	assert.Equal(t, []string{"1.0.1-1"}, treetest.Versions(t, root, "luatest"))
}

// TestPruneReportsAReplacedVersion pins the line of a version the lock moved
// away from.
func TestPruneReportsAReplacedVersion(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	root := filepath.Join(projectDir, rocksDirName)

	treetest.Install(t, root,
		treetest.Rock{Name: "checks", Version: "3.1.0-1"},
		treetest.Rock{Name: "checks", Version: "3.2.0-1", Files: map[string]string{
			"lua/checks/init.lua":  "-- checks 3.2.0\n",
			"lua/checks/extra.lua": "-- only in 3.2.0\n",
		}})

	logged := prune(t, projectDir, lockOf([]manifest.LockDependency{
		registryDep("checks", "3.1.0-1"),
	}))

	assert.Equal(t, []string{"removed checks 3.2.0-1, the lock pins 3.1.0-1"}, logged)
	assert.NoFileExists(t, filepath.Join(root, "share", "tarantool", "checks", "extra.lua"),
		"a file only the version removed has goes with it")
	assert.FileExists(t, filepath.Join(root, "share", "tarantool", "checks", "init.lua"))
}

// TestPruneRemovesTheVersionsTheLockMovedAwayFrom covers a lock that moved a
// rock to another version: the native client keeps the old one, which would
// hold the plain paths of a downgraded rock.
func TestPruneRemovesTheVersionsTheLockMovedAwayFrom(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	root := filepath.Join(projectDir, rocksDirName)

	treetest.Install(t, root,
		treetest.Rock{Name: "checks", Version: "3.1.0-1"},
		treetest.Rock{Name: "checks", Version: "3.2.0-1"},
		treetest.Rock{Name: "luatest", Version: "0.5.7-1"},
		treetest.Rock{Name: "luatest", Version: "1.0.1-1"})

	logged := prune(t, projectDir, pruneLock())

	assert.Equal(t, []string{"3.1.0-1"}, treetest.Versions(t, root, "checks"))
	assert.Equal(t, []string{"1.0.1-1"}, treetest.Versions(t, root, "luatest"),
		"the dev closure is pinned too")
	assert.Len(t, logged, 2)
	assert.FileExists(t, filepath.Join(root, "share", "tarantool", "checks", "init.lua"),
		"the plain path passes to the version kept")
}

// TestPruneKeepsWhatTheLockDoesNotPin covers the rocks that are not the
// lock's to decide: one tt test installed with no declaration, the guests and
// what their locks pin, at whatever version their install reconciled.
func TestPruneKeepsWhatTheLockDoesNotPin(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	lay := state.ProjectLayout(projectDir)

	lock := &manifest.Lock{Products: map[string]manifest.LockProduct{
		"default": {Dependencies: []manifest.LockDependency{registryDep("checks", "3.1.0-1")}},
	}}

	guestLock := manifest.Lock{
		LockVersion: manifest.LockVersion, ManifestVersion: manifest.ManifestVersion,
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{registryDep("metrics", "1.0.0-1")}},
		},
	}
	guestLockBytes, err := guestLock.Marshal()
	require.NoError(t, err)
	require.NoError(t, state.WriteMetadata(lay, "monitoring",
		[]byte("manifest_version = '0.1'\n[package]\nname = 'monitoring'\n"),
		guestLockBytes, "2.0.0"))

	treetest.Install(t, lay.Tree,
		treetest.Rock{Name: "checks", Version: "3.1.0-1"},
		treetest.Rock{Name: "luatest", Version: "1.0.1-1"},
		treetest.Rock{Name: "monitoring", Version: "2.0.0-1"},
		treetest.Rock{Name: "metrics", Version: "1.4.0-1"})

	assert.Empty(t, prune(t, projectDir, lock))

	for name, version := range map[string]string{
		"checks": "3.1.0-1", "luatest": "1.0.1-1", "monitoring": "2.0.0-1", "metrics": "1.4.0-1",
	} {
		assert.Equal(t, []string{version}, treetest.Versions(t, lay.Tree, name), name)
	}

	assert.Len(t, prune(t, projectDir, lock, registryDep("luatest", "1.0.2-1")), 1,
		"a rock a caller resolved is pinned")
}

// TestPruneRemovesAnOrphanRockDir covers what an interrupted install leaves:
// a rock dir the manifest does not list.
func TestPruneRemovesAnOrphanRockDir(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()
	root := filepath.Join(projectDir, rocksDirName)

	treetest.Install(t, root, treetest.Rock{Name: "checks", Version: "3.1.0-1"})

	orphan := filepath.Join(root, "share", "tarantool", "rocks", "half", "1.0.0-1")
	require.NoError(t, os.MkdirAll(orphan, 0o750))

	prune(t, projectDir, pruneLock())

	assert.NoDirExists(t, orphan)
	assert.Equal(t, []string{"3.1.0-1"}, treetest.Versions(t, root, "checks"))
}

// TestPruneCreatesNoTree pins that a project with no tree gets none.
func TestPruneCreatesNoTree(t *testing.T) {
	t.Parallel()

	projectDir := t.TempDir()

	assert.Empty(t, prune(t, projectDir, pruneLock()))
	assert.NoDirExists(t, filepath.Join(projectDir, rocksDirName))
}
