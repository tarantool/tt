//nolint:testpackage // white-box: exercises the unexported devOnlyRocks directly.
package pack

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
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

// devRocksTree lays a .rocks/ tree in the shape a build leaves behind, with the
// dev closure already materialized into it - which is the situation the pack
// exclusion exists for: .rocks/ is the developer's standing tree, so the dev
// rocks are there whether or not this particular pack installed them.
//
// luatest carries the awkward shapes on purpose: a console script in bin/ and a
// bare share/tarantool/<name>.lua rather than a directory. Both are outside the
// name-keyed directories, and both are what a rock_manifest is read for.
func devRocksTree(t *testing.T, projectDir string) string {
	t.Helper()

	tree := filepath.Join(projectDir, ".rocks")

	writeTree(t, tree, map[string]string{
		// The package's own laid-out component files.
		"share/tarantool/my-app/init.lua":    "-- app",
		"share/tarantool/my-app/version.lua": "-- version",
		"lib/tarantool/my-app/fast.so":       "ELF",

		// Runtime closure.
		"share/tarantool/inspect/init.lua": "-- inspect",
		"share/tarantool/rocks/inspect/3.1.3-1/rock_manifest": rockManifest(
			`lua = { ["inspect/init.lua"] = "d0" }`),
		"share/tarantool/checks.lua": "-- checks",
		"share/tarantool/rocks/checks/3.1.0-1/rock_manifest": rockManifest(
			`lua = { ["checks.lua"] = "d1" }`),

		// Dev closure.
		"share/tarantool/luatest.lua":        "-- luatest",
		"share/tarantool/luatest/runner.lua": "-- runner",
		"lib/tarantool/luatest/helper.so":    "ELF",
		"bin/luatest":                        "#!/usr/bin/env tarantool",
		"share/tarantool/luacov/init.lua":    "-- luacov",
		"share/tarantool/rocks/luacov/0.15.0-1/rock_manifest": rockManifest(
			`lua = { ["luacov/init.lua"] = "d3" }`),
		"share/tarantool/rocks/luatest/1.0.1-1/rock_manifest": rockManifest(
			`lua = { ["luatest.lua"] = "d4", ["luatest/runner.lua"] = "d5" },` + "\n" +
				`lib = { ["luatest/helper.so"] = "d6" },` + "\n" +
				`bin = { ["luatest"] = "d7" }`),
	})

	return tree
}

// rockManifest wraps a rock_manifest body in the assignments-mode envelope
// LuaRocks writes on disk.
func rockManifest(body string) string {
	return "rock_manifest = {\n" + body + "\n}\n"
}

// packArchive stages a tree and writes the archive, returning its entry names.
// This is the archive-selection path in full: whatever it drops here is what a
// .tt file does not carry.
func packArchive(t *testing.T, req stageRequest) []string {
	t.Helper()

	stageDir := t.TempDir()
	require.NoError(t, stage(stageDir, req))

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

// TestPackWithoutDepsAlsoDropsDevRocks pins that the two filters compose. A
// dev-only rock would not normally survive --without-deps anyway, since only
// the package's own namespaces are copied - so the case that matters is a dev
// rock sharing a name with one of them, where the namespace filter alone lets
// it through.
func TestPackWithoutDepsAlsoDropsDevRocks(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)
	tree := devRocksTree(t, projectDir)

	// A dev-only rock installing into the package's own namespace directory.
	writeTree(t, tree, map[string]string{
		"share/tarantool/my-app/devtool.lua": "-- devtool",
		"share/tarantool/rocks/devtool/1.0.0-1/rock_manifest": rockManifest(
			`lua = { ["my-app/devtool.lua"] = "d8" }`),
	})

	lock := devTreeLock()

	lock.DevDependencies = append(lock.DevDependencies,
		manifest.LockDependency{Name: "devtool", Version: "1.0.0-1", Source: "registry"})

	req := baseRequest(projectDir, man, tree)

	req.WithDeps = false
	req.DevOnly = devOnlyRocks(lock)

	names := packArchive(t, req)

	assert.Contains(t, names, ".rocks/share/tarantool/my-app/init.lua",
		"--without-deps must still keep the package's own namespace")
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

// TestDevRockFilterFallsBackWithoutRockManifest pins the degraded path: a rock
// whose rock_manifest is missing is excluded by the name-keyed directories
// instead, the same ones cli/manifest/state.RockPaths uses.
func TestDevRockFilterFallsBackWithoutRockManifest(t *testing.T) {
	t.Parallel()

	tree := t.TempDir()

	skip := devRockFilter(tree, []manifest.LockDependency{
		{Name: "luatest", Version: "1.0.1-1", Source: "registry"},
	})
	require.NotNil(t, skip)

	assert.True(t, skip("share/tarantool/luatest"))
	assert.True(t, skip("share/tarantool/luatest/runner.lua"))
	assert.True(t, skip("lib/tarantool/luatest/helper.so"))
	assert.True(t, skip("share/tarantool/rocks/luatest"))

	// Under-excluding is the safe direction: without a rock_manifest the flat
	// module and the console script are unknown and are kept.
	assert.False(t, skip("share/tarantool/luatest.lua"))
	assert.False(t, skip("bin/luatest"))
	assert.False(t, skip("share/tarantool/inspect/init.lua"))

	assert.Nil(t, devRockFilter(tree, nil))
}

// TestStageRocksMissingTreeWithDevOnly guards the interaction of the filter
// with the pure-metadata case: no tree to copy is still not an error.
func TestStageRocksMissingTreeWithDevOnly(t *testing.T) {
	t.Parallel()

	projectDir, man := testProject(t, nil)

	req := baseRequest(projectDir, man, filepath.Join(projectDir, ".rocks"))

	req.DevOnly = devOnlyRocks(devTreeLock())

	stageDir := t.TempDir()
	require.NoError(t, stage(stageDir, req))

	_, err := os.Stat(filepath.Join(stageDir, rocksDirName))
	assert.True(t, os.IsNotExist(err))
}
