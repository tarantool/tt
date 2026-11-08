package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// checkTree checks that the manifest of the tree at root lists every rock it
// holds and every file they deployed, and no other.
func checkTree(t *testing.T, root string) {
	t.Helper()

	rocksTree, err := tree.Open(luarocks.Config{Tree: root})
	require.NoError(t, err)

	report, err := rocksTree.Check(t.Context(), tree.CheckOpts{})
	require.NoError(t, err)
	assert.True(t, report.OK(), "%v", report.Issues)
}

// TestInstallGraftsIntoTheProjectsTree: the guest's rocks join the project's
// tree and its manifest, which keeps listing the rocks installed before it.
func TestInstallGraftsIntoTheProjectsTree(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree := filepath.Join(dir, ".rocks")
	treetest.Install(t, tree, treetest.Rock{Name: "checks", Version: "3.1.0"})

	_, err := installInto(t, dir, Options{Scope: ScopeProject},
		withRocks("router", "1.0.0", "3.0.4", ">=3.0.0").build(t))
	require.NoError(t, err)

	assert.Equal(t, []string{"3.1.0-1"}, treetest.Versions(t, tree, "checks"))
	assert.Equal(t, []string{"3.0.4-1"}, treetest.Versions(t, tree, sharedDep))
	assert.Equal(t, []string{"1.0.0-1"}, treetest.Versions(t, tree, "router"))
	checkTree(t, tree)
}

// TestInstallUpgradeReplacesTheGuestsRock: an upgrade replaces the guest's
// rock, and a file the new version no longer ships goes with the old one.
func TestInstallUpgradeReplacesTheGuestsRock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree := filepath.Join(dir, ".rocks")

	older := archiveSpec{name: "router", version: "1.0.0", rocks: []treetest.Rock{{
		Name: "router", Version: "1.0.0", Files: map[string]string{
			"lua/router/init.lua": "return 1\n", "lua/router/old.lua": "return 0\n",
		},
	}}}
	newer := archiveSpec{name: "router", version: "1.1.0", rocks: []treetest.Rock{{
		Name: "router", Version: "1.1.0", Files: map[string]string{
			"lua/router/init.lua": "return 2\n",
		},
	}}}

	_, err := installInto(t, dir, Options{Scope: ScopeProject}, older.build(t))
	require.NoError(t, err)

	_, err = installInto(t, dir, Options{Scope: ScopeProject, Upgrade: true}, newer.build(t))
	require.NoError(t, err)

	assert.Equal(t, []string{"1.1.0-1"}, treetest.Versions(t, tree, "router"))
	assert.NoFileExists(t, filepath.Join(tree, "share", "tarantool", "router", "old.lua"))
	assert.Equal(t, "return 2\n",
		readFile(t, filepath.Join(tree, "share", "tarantool", "router", "init.lua")))
	checkTree(t, tree)
}

// TestInstallTakesInAnArchiveOfAnOlderTt: an archive an older tt packed
// carries the package's files laid out by hand, no rock of its tree; they are
// installed as the package's rock, with a warning.
func TestInstallTakesInAnArchiveOfAnOlderTt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tree := filepath.Join(dir, ".rocks")

	archive := archiveSpec{
		name: "router", version: "1.0.0-dev.2+gabc", withRuntime: "3.0.5",
		deps:     map[string]string{sharedDep: ">=3.0.0"},
		lockDeps: []LockDep{{Name: sharedDep, Version: "3.0.4", Source: "registry"}},
		files: mergeFiles(rockFiles(sharedDep, "3.0.4"), map[string]string{
			".rocks/share/tarantool/router/init.lua":    "return 1\n",
			".rocks/share/tarantool/router/version.lua": "return {}\n",
			".rocks/lib/tarantool/router/fast.so":       "so\n",
			".rocks/bin/gateway":                        "#!/bin/sh\n",
		}),
	}

	var warned []string

	_, err := installInto(t, dir, Options{
		Scope: ScopeProject, Warn: func(msg string) { warned = append(warned, msg) },
	}, archive.build(t))
	require.NoError(t, err)

	assert.Equal(t, []string{"1.0.0.dev.2.gabc-1"}, treetest.Versions(t, tree, "router"))
	assert.FileExists(t, filepath.Join(tree, "lib", "tarantool", "router", "fast.so"))
	assert.FileExists(t, filepath.Join(tree, "bin", "gateway"))
	assert.Equal(t, []string{"3.0.4-1"}, treetest.Versions(t, tree, sharedDep))
	checkTree(t, tree)

	require.NotEmpty(t, warned)
	assert.Contains(t, strings.Join(warned, "\n"), "did not pack it as a rock")
}

// TestInstallReplacesAGuestAnOlderTtInstalled: a guest an older tt laid out
// by hand in the project's tree is replaced by its rock, its files gone.
func TestInstallReplacesAGuestAnOlderTtInstalled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lay := state.ProjectLayout(dir)

	old := archiveSpec{name: "router", version: "1.0.0"}
	require.NoError(t, state.WriteMetadata(lay, "router", []byte(old.manifestTOML()),
		old.lockBytes(t), "1.0.0"))

	stale := filepath.Join(lay.Share, "router", "stale.lua")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o750))
	require.NoError(t, os.WriteFile(stale, []byte("return 0\n"), 0o600))

	_, err := installInto(t, dir, Options{Scope: ScopeProject, Force: true},
		appSpec("router", "1.1.0").build(t))
	require.NoError(t, err)

	assert.NoFileExists(t, stale)
	assert.Equal(t, []string{"1.1.0-1"}, treetest.Versions(t, lay.Tree, "router"))
	checkTree(t, lay.Tree)
}

// TestInstallVersionRewrapsCommands: the commands of an installed version
// run the tarantool the archive bundles, from the version's tree, whatever
// the archive's tree named.
func TestInstallVersionRewrapsCommands(t *testing.T) {
	root := userRoot(t)

	archive := archiveSpec{
		name: "my-app", version: "1.0.0", withRuntime: "3.0.5",
		rocks: []treetest.Rock{
			{Name: "my-app", Version: "1.0.0", Files: map[string]string{
				"lua/my-app/init.lua": "return 1\n",
			}},
			{Name: "luatest", Version: "1.0.1", Files: map[string]string{
				"lua/luatest.lua": "return {}\n",
				"bin/luatest":     "#!/usr/bin/env tarantool\nprint(1)\n",
			}},
		},
	}

	_, err := installInto(t, t.TempDir(), Options{
		Scope: ScopeUser, Tarantool: rocks.TarantoolInfo{Executable: "/usr/bin/tarantool"},
	}, archive.build(t))
	require.NoError(t, err)

	version := versionPath(root, "1.0.0")
	wrapper := readFile(t, filepath.Join(version, ".rocks", "bin", "luatest"))

	assert.Contains(t, wrapper, filepath.Join(root, "versions", "tarantool", "tarantool_3.0.5"),
		"the wrapper runs the bundled tarantool")
	assert.Contains(t, wrapper, filepath.Join(version, ".rocks"),
		"the wrapper names the tree where it is")
	checkTree(t, filepath.Join(version, ".rocks"))
}
