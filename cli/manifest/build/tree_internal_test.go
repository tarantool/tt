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

// prune runs pruneTree over the project at projectDir and returns what it
// logged.
func prune(t *testing.T, projectDir string, lock *manifest.Lock,
	extra ...manifest.LockDependency,
) []string {
	t.Helper()

	var logged []string

	cfg := luarocks.Config{Tree: filepath.Join(projectDir, rocksDirName), WorkingDir: projectDir}

	require.NoError(t, pruneTree(t.Context(), cfg, projectDir, lock, extra,
		func(msg string) { logged = append(logged, msg) }))

	return logged
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
