package rocktree_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/treetest"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

func TestRockVersion(t *testing.T) {
	t.Parallel()

	for semver, want := range map[string]string{
		"1.2.0":                      "1.2.0-1",
		"v1.2.0":                     "1.2.0-1",
		"1.2.1-dev.3+gabc1234":       "1.2.1.dev.3.gabc1234-1",
		"0.0.0-dev.5+gabc1234.dirty": "0.0.0.dev.5.gabc1234.dirty-1",
		"2.0.0-rc.1":                 "2.0.0.rc.1-1",
		"1.2.0+dirty":                "1.2.0.dirty-1",
	} {
		got, err := rocktree.RockVersion(semver)
		require.NoError(t, err, semver)
		assert.Equal(t, want, got, semver)
	}

	for _, bad := range []string{"", "1..2", "1.2.0-", "../1", "1.2 3"} {
		_, err := rocktree.RockVersion(bad)
		assert.Error(t, err, bad)
	}
}

func TestRockspec(t *testing.T) {
	t.Parallel()

	spec, err := rocktree.Rockspec(rocktree.Package{
		Name: "my-app", Version: "1.2.1-dev.3+gabc1234",
		Summary:      "the \"app\"\\ of\nmine, ça va",
		Dependencies: map[string]string{"metrics": "1.0.0-1", "checks": "3.1.0-1"},
	})
	require.NoError(t, err)

	assert.Equal(t, "my-app", spec.Package)
	assert.Equal(t, "1.2.1.dev.3.gabc1234-1", spec.Version)
	assert.Equal(t, "the \"app\"\\ of\nmine, ça va", spec.Description.Summary)
	require.Len(t, spec.Dependencies, 2)
	assert.Equal(t, "checks", spec.Dependencies[0].Name)
	assert.Equal(t, "metrics", spec.Dependencies[1].Name)
	require.NotNil(t, spec.Deploy.WrapBinScripts)
	assert.False(t, *spec.Deploy.WrapBinScripts, "a program is deployed as it is")
	assert.NotEmpty(t, spec.RawSource)
}

// TestPackageDependencies: the rock of a package depends on what its product
// declares at the versions the lock pins, and not on a path dependency with
// nothing to build, which the lock pins at no version and installs no rock.
func TestPackageDependencies(t *testing.T) {
	t.Parallel()

	man, _, err := manifest.ParseManifest([]byte(`manifest_version = '0.1'
[package]
name = 'my-app'
[platform]
tarantool = '>=3.0.0'
tt = '>=3.0.0'
[dependencies]
metrics = '>=1.0.0'
leaf = { source = 'path', path = '../leaf' }
contrib = { source = 'path', path = '../contrib' }
[components.lua]
path = '.'
[components.lua.dependencies]
checks = '>=3.0.0'
`))
	require.NoError(t, err)

	prod := manifest.LockProduct{Dependencies: []manifest.LockDependency{
		{Name: "metrics", Version: "1.0.0-1", Source: "registry"},
		{Name: "checks", Version: "3.1.0-1", Source: "registry"},
		{Name: "leaf", Version: "", Source: "path", Path: "../leaf"},
		{Name: "contrib", Version: "0.3.0-1", Source: "path", Path: "../contrib"},
		{Name: "transitive", Version: "2.0.0-1", Source: "registry"},
	}}

	deps := rocktree.PackageDependencies(man, manifest.ImplicitProduct, prod)
	assert.Equal(t, map[string]string{
		"metrics": "1.0.0-1", "checks": "3.1.0-1", "contrib": "0.3.0-1",
	}, deps)

	spec, err := rocktree.Rockspec(rocktree.Package{
		Name: "my-app", Version: "1.0.0", Dependencies: deps,
	})
	require.NoError(t, err)
	assert.Len(t, spec.Dependencies, 3)
}

// stageFiles lays files out under a fresh stage.
func stageFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	stage := t.TempDir()

	for rel, body := range files {
		path := filepath.Join(stage, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	}

	return stage
}

// installPackage installs stage as the rock of my-app at version into root.
func installPackage(t *testing.T, root, version, stage string) error {
	t.Helper()

	spec, err := rocktree.Rockspec(rocktree.Package{Name: "my-app", Version: version})
	require.NoError(t, err)

	rocksTree := openTree(t, root)

	return rocksTree.Update(t.Context(), func(change *tree.Tx) error {
		return rocktree.InstallPackage(change, root, spec, stage)
	})
}

// TestInstallPackage installs the package's rock twice, at two versions: the
// second replaces the first, and every file is at its plain path, the program
// as it was built.
func TestInstallPackage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	require.NoError(t, installPackage(t, root, "1.0.0", stageFiles(t, map[string]string{
		"lua/my-app/init.lua": "return 1\n",
		"lua/my-app/old.lua":  "return 0\n",
	})))

	require.NoError(t, installPackage(t, root, "1.1.0", stageFiles(t, map[string]string{
		"lua/my-app/init.lua":   "return 2\n",
		"lib/my-app/native.so":  "so\n",
		"bin/prog":              "#!/bin/sh\nexit 0\n",
		"lua/my-app/data.json":  "{}\n",
		"lua/my-app/sub/x.lua":  "return 3\n",
		"lua/my-app/sub/y.wasm": "\x00asm",
	})))

	assert.Equal(t, []string{"1.1.0-1"}, treetest.Versions(t, root, "my-app"))
	assert.NoFileExists(t, filepath.Join(root, "share", "tarantool", "my-app", "old.lua"),
		"a file the new version does not ship goes with the old one")

	data, err := os.ReadFile(filepath.Join(root, "share", "tarantool", "my-app", "init.lua"))
	require.NoError(t, err)
	assert.Equal(t, "return 2\n", string(data))

	assert.FileExists(t, filepath.Join(root, "lib", "tarantool", "my-app", "native.so"))
	assert.FileExists(t, filepath.Join(root, "share", "tarantool", "my-app", "sub", "y.wasm"))

	prog, err := os.ReadFile(filepath.Join(root, "bin", "prog"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\nexit 0\n", string(prog), "no wrapper")

	info, err := os.Stat(filepath.Join(root, "bin", "prog"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "the program stays executable")

	report, err := openTree(t, root).Check(t.Context(), tree.CheckOpts{})
	require.NoError(t, err)
	assert.True(t, report.OK(), "%v", report.Issues)
}

// TestInstallPackageTakesOverWhatAnOlderTtLaidOut covers the first build
// after an upgrade of tt: the files of the package are in the tree, owned by
// no rock, and the package's rock takes their place instead of backing them
// up.
func TestInstallPackageTakesOverWhatAnOlderTtLaidOut(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	laid := filepath.Join(root, "share", "tarantool", "my-app", "init.lua")

	require.NoError(t, os.MkdirAll(filepath.Dir(laid), 0o750))
	require.NoError(t, os.WriteFile(laid, []byte("return 1\n"), 0o600))

	require.NoError(t, installPackage(t, root, "1.0.0", stageFiles(t, map[string]string{
		"lua/my-app/init.lua": "return 2\n",
	})))

	data, err := os.ReadFile(laid)
	require.NoError(t, err)
	assert.Equal(t, "return 2\n", string(data))
	assert.NoFileExists(t, laid+"~", "no backup")
}

// TestInstallPackageRefusesAFileAnotherRockHolds pins that the package does
// not take the plain path of a dependency's file: require would load one of
// the two, whichever the tree's conflict rules chose.
func TestInstallPackageRefusesAFileAnotherRockHolds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// Of two rocks, LuaRocks gives the plain path to the one whose name sorts
	// first: a-checks would keep it, z-checks would lose it to the package.
	treetest.Install(t, root,
		treetest.Rock{Name: "a-checks", Version: "3.1.0",
			Files: map[string]string{"lua/a.lua": "return {}\n"}},
		treetest.Rock{Name: "z-checks", Version: "3.1.0",
			Files: map[string]string{"lua/z.lua": "return {}\n"}})

	for _, file := range []string{"lua/a.lua", "lua/z.lua"} {
		err := installPackage(t, root, "1.0.0", stageFiles(t, map[string]string{
			file: "return 1\n",
		}))
		require.ErrorIs(t, err, tree.ErrNotPlain, file)
		assert.Empty(t, treetest.Versions(t, root, "my-app"))
	}
}

// TestStageUntracked lays the files no rock owns out as an unpacked binary
// rock, backups and files outside the deployed directories aside.
func TestStageUntracked(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	stage := t.TempDir()

	untracked := make([]string, 0, 5)

	for _, rel := range []string{
		"share/tarantool/my-app/init.lua", "lib/tarantool/my-app/native.so", "bin/prog",
		"share/tarantool/my-app/init.lua~", "share/doc/readme",
	} {
		path := filepath.Join(src, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(rel), 0o700))

		untracked = append(untracked, path)
	}

	staged, err := rocktree.StageUntracked(src, untracked, stage)
	require.NoError(t, err)
	assert.Equal(t, 3, staged)

	assert.FileExists(t, filepath.Join(stage, "lua", "my-app", "init.lua"))
	assert.FileExists(t, filepath.Join(stage, "lib", "my-app", "native.so"))

	info, err := os.Stat(filepath.Join(stage, "bin", "prog"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}
