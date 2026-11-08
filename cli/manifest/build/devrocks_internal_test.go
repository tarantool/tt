package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// installRock installs a rock into the project's tree as LuaRocks records one:
// its rock dir and its entry in the tree manifest, which is what marks a rock
// installed, so it is what the skip has to read.
func installRock(t *testing.T, projectDir, name, version string) {
	t.Helper()

	treetest.Install(t, filepath.Join(projectDir, rocksDirName), treetest.Rock{
		Name: name, Version: version, Files: nil, Dependencies: nil,
	})
}

// projectView reads the tree of the project at projectDir.
func projectView(t *testing.T, projectDir string) *tree.View {
	t.Helper()

	view, err := state.InspectTree(t.Context(), state.ProjectLayout(projectDir))
	require.NoError(t, err)

	return view
}

// registryDep is one resolved registry entry of a closure.
func registryDep(name, version string) manifest.LockDependency {
	return manifest.LockDependency{Name: name, Version: version, Source: sourceRegistry}
}

func TestNotInstalledKeepsRocksTheTreeDoesNotHold(t *testing.T) {
	projectDir := t.TempDir()

	closure := []manifest.LockDependency{
		registryDep("checks", "3.1.0-1"),
		registryDep("luatest", "1.0.1-1"),
	}

	missing := notInstalled(projectView(t, projectDir), closure)
	assert.Equal(t, closure, missing)
}

// The skip is what keeps a second run of the same command from re-downloading
// its whole closure: this resolution happens on every run, unlike the lock's,
// which a build materializes once per lock change.
func TestNotInstalledSkipsAnInstalledVersion(t *testing.T) {
	projectDir := t.TempDir()
	installRock(t, projectDir, "checks", "3.1.0-1")

	missing := notInstalled(projectView(t, projectDir), []manifest.LockDependency{
		registryDep("checks", "3.1.0-1"),
		registryDep("luatest", "1.0.1-1"),
	})
	require.Len(t, missing, 1)
	assert.Equal(t, "luatest", missing[0].Name)
}

// A different version installed is not the version resolved, and the tree holds
// one version of a rock, so it has to be replaced rather than left alone.
func TestNotInstalledKeepsADifferentInstalledVersion(t *testing.T) {
	projectDir := t.TempDir()
	installRock(t, projectDir, "luatest", "0.5.7-1")

	missing := notInstalled(projectView(t, projectDir), []manifest.LockDependency{
		registryDep("luatest", "1.0.1-1"),
	})
	require.Len(t, missing, 1)
	assert.Equal(t, "1.0.1-1", missing[0].Version)
}

// A path dependency's version says nothing about its contents - the lock pins
// it by content hash for exactly that reason - so it is always rebuilt.
func TestNotInstalledAlwaysRebuildsAPathDependency(t *testing.T) {
	projectDir := t.TempDir()
	installRock(t, projectDir, "helper", "0.1.0-1")

	missing := notInstalled(projectView(t, projectDir), []manifest.LockDependency{
		{Name: "helper", Version: "0.1.0-1", Source: sourcePath, Path: "helper"},
	})
	assert.Len(t, missing, 1)
}

// An empty requirement set is not a reason to read the manifest, load the lock
// or build a registry client, so the call returns before any of it - which is
// what lets a caller pass whatever it has without special-casing "nothing".
func TestEnsureDevRocksWithoutExtraRequirementsDoesNothing(t *testing.T) {
	projectDir := t.TempDir()

	err := EnsureDevRocks(context.Background(), Options{ProjectDir: projectDir}, nil)
	require.NoError(t, err)

	entries, err := os.ReadDir(projectDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// The lock is read and never written, so a project that has never resolved is
// an error here instead of an implicit resolution that would rewrite it.
func TestEnsureDevRocksNeedsALock(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectDir, manifestFileName), []byte(minimalManifest), 0o600))

	err := EnsureDevRocks(context.Background(), Options{ProjectDir: projectDir},
		map[string]manifest.Dependency{"luatest": {Source: sourceRegistry, Version: "*"}})

	require.ErrorIs(t, err, errNoLock)
	assert.NoFileExists(t, filepath.Join(projectDir, lockFileName))
}

// minimalManifest is a manifest that validates, with no dependencies of any
// kind, so the only requirement in play is the caller's.
const minimalManifest = `manifest_version = '0.1'
[package]
name = 'app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[components.app]
path = '.'
[products.default]
components = ['app']
default = true
`
