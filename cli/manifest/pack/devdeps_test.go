//nolint:testpackage // white-box: exercises the unexported devOnlyRocks directly.
package pack

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
)

// devTreeLock is the lock the tree below was materialized from: one runtime
// rock, one rock in both closures, and two rocks only the dev closure brings.
func devTreeLock() *manifest.Lock {
	return &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "inspect", Version: "3.1.3-1", Source: "registry"},
				{Name: "checks", Version: "3.1.0-1", Source: "registry"},
			}},
		},
		DevDependencies: []manifest.LockDependency{
			// Shared with the product closure at the same version, which is
			// what the resolver's pinning guarantees. It must survive the pack.
			{Name: "checks", Version: "3.1.0-1", Source: "registry"},
			{Name: "luatest", Version: "1.0.1-1", Source: "registry"},
			{Name: "luacov", Version: "0.15.0-1", Source: "registry"},
		},
	}
}

// devRocksTree installs a .rocks/ tree in the shape a build leaves behind,
// with the dev closure already materialized into it - which is the situation
// the pack exclusion exists for: .rocks/ is the developer's standing tree, so
// the dev rocks are there whether or not this particular pack installed them.
//
// luatest carries the awkward shapes on purpose: a console script in bin/ and a
// bare share/tarantool/<name>.lua rather than a directory, which a rock's
// files, not its name, place.
func devRocksTree(t *testing.T, projectDir string) string {
	t.Helper()

	tree := filepath.Join(projectDir, ".rocks")

	treetest.Install(t, tree,
		// The package's own rock.
		treetest.Rock{Name: "my-app", Version: "1.0.0", Files: map[string]string{
			"lua/my-app/init.lua":    "-- app",
			"lua/my-app/version.lua": "-- version",
			"lib/my-app/fast.so":     "ELF",
		}},
		// Runtime closure.
		treetest.Rock{Name: "inspect", Version: "3.1.3-1", Files: map[string]string{
			"lua/inspect/init.lua": "-- inspect",
		}},
		treetest.Rock{Name: "checks", Version: "3.1.0-1", Files: map[string]string{
			"lua/checks.lua": "-- checks",
		}},
		// Dev closure.
		treetest.Rock{Name: "luatest", Version: "1.0.1-1", Files: map[string]string{
			"lua/luatest.lua":        "-- luatest",
			"lua/luatest/runner.lua": "-- runner",
			"lib/luatest/helper.so":  "ELF",
			"bin/luatest":            "#!/usr/bin/env tarantool\n",
		}},
		treetest.Rock{Name: "luacov", Version: "0.15.0-1", Files: map[string]string{
			"lua/luacov/init.lua": "-- luacov",
		}})

	return tree
}

// packArchive stages a tree and writes the archive, returning its entry names.
// This is the archive-selection path in full: whatever it drops here is what a
// .tt file does not carry.
func packArchive(t *testing.T, req stageRequest) []string {
	t.Helper()

	stageDir := t.TempDir()
	require.NoError(t, stage(t.Context(), stageDir, req))

	dest := filepath.Join(t.TempDir(), "my-app-1.0.0-any.tt")
	_, err := writeArchive(stageDir, dest)
	require.NoError(t, err)

	entries := readArchive(t, dest)

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// TestPackExcludesDevOnlyRocks is the acceptance test for the exclusion, in the
// shape that can actually fail: the tree handed to pack already holds the dev
// closure, exactly as a tt package build run earlier that day would have left
// it, and the assertion is on the bytes of the archive rather than on the
// staging directory.
func TestPackExcludesDevOnlyRocks(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)
	tree := devRocksTree(t, projectDir)

	req := baseRequest(projectDir, man, tree)

	req.DevOnly = devOnlyRocks(devTreeLock())

	names := packArchive(t, req)

	// The runtime closure and the package's own files are all there.
	assert.Contains(t, names, ".rocks/share/tarantool/inspect/init.lua")
	assert.Contains(t, names, ".rocks/share/tarantool/checks.lua")
	assert.Contains(t, names, ".rocks/share/tarantool/my-app/init.lua")
	assert.Contains(t, names, ".rocks/lib/tarantool/my-app/fast.so")

	// A rock both closures hold is a runtime rock and survives, metadata
	// included: excluding it would break the archive it belongs to.
	assert.Contains(t, names, ".rocks/share/tarantool/rocks/checks/3.1.0-1/rock_manifest")

	// Every dev-only file is gone - including the two shapes the name-keyed
	// directories would have missed.
	for _, gone := range []string{
		".rocks/share/tarantool/luatest.lua",
		".rocks/share/tarantool/luatest/runner.lua",
		".rocks/lib/tarantool/luatest/helper.so",
		".rocks/bin/luatest",
		".rocks/share/tarantool/luacov/init.lua",
		".rocks/share/tarantool/rocks/luatest/1.0.1-1/rock_manifest",
		".rocks/share/tarantool/rocks/luacov/0.15.0-1/rock_manifest",
	} {
		assert.NotContains(t, names, gone, "dev-only file must not reach the archive")
	}
}

// TestPackWithoutDevDependenciesIsUnchanged is the control: with no dev closure
// in the lock the same tree packs whole, so the test above is measuring the
// exclusion rather than some other thing that drops files.
func TestPackWithoutDevDependenciesIsUnchanged(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)
	tree := devRocksTree(t, projectDir)

	req := baseRequest(projectDir, man, tree)

	req.DevOnly = devOnlyRocks(&manifest.Lock{
		Products: devTreeLock().Products,
	})

	names := packArchive(t, req)

	assert.Contains(t, names, ".rocks/share/tarantool/luatest.lua")
	assert.Contains(t, names, ".rocks/bin/luatest")
}

// TestPackWithoutDepsAlsoDropsDevRocks pins that a dev rock installing into
// the package's own namespace directory is not taken for the package's: the
// package's rock is what --without-deps packs, not a directory.
func TestPackWithoutDepsAlsoDropsDevRocks(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)
	tree := devRocksTree(t, projectDir)

	// A dev-only rock installing into the package's own namespace directory.
	treetest.Install(t, tree, treetest.Rock{Name: "devtool", Version: "1.0.0-1",
		Files: map[string]string{"lua/my-app/devtool.lua": "-- devtool"}})

	lock := devTreeLock()

	lock.DevDependencies = append(lock.DevDependencies,
		manifest.LockDependency{Name: "devtool", Version: "1.0.0-1", Source: "registry"})

	req := baseRequest(projectDir, man, tree)

	req.WithDeps = false
	req.DevOnly = devOnlyRocks(lock)

	names := packArchive(t, req)

	assert.Contains(t, names, ".rocks/share/tarantool/my-app/init.lua",
		"--without-deps must still keep the package's own rock")
	assert.NotContains(t, names, ".rocks/share/tarantool/my-app/devtool.lua",
		"a dev rock inside the package namespace must still be excluded")
	assert.NotContains(t, names, ".rocks/share/tarantool/inspect/init.lua",
		"--without-deps still drops foreign runtime rocks")
}

// TestDevOnlyRocksExcludesSharedNames covers the ownership rule on its own: a
// rock any product's closure holds is not dev-only, however the dev table
// declares it.
func TestDevOnlyRocksExcludesSharedNames(t *testing.T) {
	t.Parallel()

	lock := &manifest.Lock{
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "inspect", Version: "3.1.3-1", Source: "registry"},
			}},
			// A second product the archive is not for. Its rocks are in the
			// tree all the same, so they are not dev-only either.
			"minimal": {Dependencies: []manifest.LockDependency{
				{Name: "checks", Version: "3.1.0-1", Source: "registry"},
			}},
		},
		DevDependencies: []manifest.LockDependency{
			{Name: "inspect", Version: "3.1.3-1", Source: "registry"},
			{Name: "checks", Version: "3.1.0-1", Source: "registry"},
			{Name: "luatest", Version: "1.0.1-1", Source: "registry"},
		},
	}

	devOnly := devOnlyRocks(lock)
	require.Len(t, devOnly, 1)
	assert.Equal(t, "luatest", devOnly[0].Name)

	assert.Nil(t, devOnlyRocks(nil))
	assert.Nil(t, devOnlyRocks(&manifest.Lock{Products: lock.Products}))
}

// TestStageRocksMissingTreeWithDevOnly guards the interaction of the filter
// with the pure-metadata case: no tree to copy is still not an error.
func TestStageRocksMissingTreeWithDevOnly(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)

	req := baseRequest(projectDir, man, filepath.Join(projectDir, ".rocks"))

	req.DevOnly = devOnlyRocks(devTreeLock())

	stageDir := t.TempDir()
	require.NoError(t, stage(t.Context(), stageDir, req))

	_, err := os.Stat(filepath.Join(stageDir, rocksDirName))
	assert.True(t, os.IsNotExist(err))
}
