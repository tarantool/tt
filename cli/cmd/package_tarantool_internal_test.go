package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/manifest/build/backend"
	"github.com/tarantool/tt/v3/cli/manifest/pack"
)

// luaPackageManifest is a package of Lua alone, which builds with no
// Tarantool.
const luaPackageManifest = `manifest_version = '0.1'

[package]
name = 'lualib'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.lualib]
path = 'lualib'
`

// luaCComponent is a lua-c component to add to luaPackageManifest, which
// builds with the headers of a Tarantool only.
const luaCComponent = `
[components.native]
path = 'native'

[components.native.build]
backend = 'lua-c'
module = 'fast'
sources = ['fast.c']
`

// withoutTarantool runs one test as a tt that found no tarantool, neither in
// the project nor in PATH, working on the project in dir, with the flags of
// tt package build and pack at their defaults but --format and --out, which
// are formats and out. The command context and the flags are package state,
// so the tests that call it cannot run in parallel.
func withoutTarantool(t *testing.T, dir string, formats []string, out string) {
	t.Helper()

	previous := cmdCtx
	product, locked, rebuild := packageProduct, packageLocked, packageRebuild
	withoutDeps, key, registries := packageWithoutDeps, packageIntegrityKey, packageRegistries
	previousFormats, previousOut, revision := packageFormats, packageOut, packageRevision

	t.Cleanup(func() {
		cmdCtx = previous
		packageProduct, packageLocked, packageRebuild = product, locked, rebuild
		packageWithoutDeps, packageIntegrityKey, packageRegistries = withoutDeps, key, registries
		packageFormats, packageOut, packageRevision = previousFormats, previousOut, revision
	})

	cmdCtx.Cli.TarantoolCli = cmdcontext.TarantoolCli{Executable: ""}
	cmdCtx.Cli.TarantoolPrefix = ""

	packageProduct, packageLocked, packageRebuild = "", false, false
	packageWithoutDeps, packageIntegrityKey, packageRegistries = false, "", nil
	packageFormats, packageOut, packageRevision = formats, out, 1

	withProjectDir(t, dir)

	// The package has no dependencies, so no server is asked; an empty one
	// keeps a TT_REGISTRIES of the environment out of the test.
	t.Setenv("TT_REGISTRIES", t.TempDir())
}

// writeLuaPackage writes the project of manifest at version 1.0.0 into a new
// directory and returns it.
func writeLuaPackage(t *testing.T, manifest string) string {
	t.Helper()

	dir := t.TempDir()

	files := map[string]string{
		manifestFileName:  manifest,
		"VERSION":         "1.0.0\n",
		"lualib/init.lua": "return {}\n",
		"native/fast.c":   "#include <lua.h>\n",
	}

	for name, content := range files {
		path := filepath.Join(dir, name)

		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	return dir
}

// TestPackagePackRockWithoutTarantool checks that a tt with no tarantool
// packs a package of Lua as a rock, builds it and fetches its closure, while
// the commands that cannot go without one still fail.
func TestPackagePackRockWithoutTarantool(t *testing.T) {
	dir := writeLuaPackage(t, luaPackageManifest)
	out := filepath.Join(t.TempDir(), "dist")

	withoutTarantool(t, dir, []string{pack.FormatRock}, out)

	tntInfo, err := buildTarantoolInfo()
	require.NoError(t, err)
	assert.Empty(t, tntInfo)

	_, err = tarantoolInfo()
	require.ErrorIs(t, err, ErrNoTarantool)

	require.NoError(t, runPackagePack())
	assert.FileExists(t, filepath.Join(out, "lualib-1.0.0-1.all.rock"))
	assert.FileExists(t, filepath.Join(out, "lualib-1.0.0-1.rockspec"))

	require.NoError(t, runPackage(nil, false))
	assert.FileExists(t, filepath.Join(dir, ".rocks", "share", "tarantool", "lualib", "init.lua"))

	require.NoError(t, runPackage(nil, true))
}

// TestPackagePackLuaCWithoutTarantool checks that a lua-c component fails the
// build and the pack of a tt with no tarantool before anything is built,
// saying that it has no headers to compile against and why.
func TestPackagePackLuaCWithoutTarantool(t *testing.T) {
	dir := writeLuaPackage(t, luaPackageManifest+luaCComponent)
	out := filepath.Join(t.TempDir(), "dist")

	withoutTarantool(t, dir, []string{pack.FormatRock}, out)

	const message = `building component "native": tarantool development headers ` +
		"not found: cannot compile c/lua-c component: no tarantool to take them from, " +
		"neither in .tarantool/bin of the project nor in PATH; tt binary install " +
		"tarantool installs one"

	err := runPackagePack()
	require.ErrorIs(t, err, backend.ErrMissingHeaders)
	require.EqualError(t, err, message)
	assert.NoDirExists(t, out)

	err = runPackage(nil, false)
	require.ErrorIs(t, err, backend.ErrMissingHeaders)
	require.EqualError(t, err, message)
	assert.NoDirExists(t, filepath.Join(dir, ".rocks"), "nothing was built")
}
