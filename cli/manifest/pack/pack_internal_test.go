package pack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// TestLockWithBundledStampsVersions covers the lock side of a with-deps pack:
// the resolver leaves bundled_* empty and pack fills them in.
func TestLockWithBundledStampsVersions(t *testing.T) {
	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.0.0",
		ManifestHash:    "sha256:abc",
	}

	out, err := lockWithBundled(lock, BundledVersions{
		Tarantool: "3.0.5", Tt: "3.2.0", Tcm: "1.5.2",
	})
	require.NoError(t, err)

	parsed, err := manifest.ParseLock(out)
	require.NoError(t, err)

	assert.Equal(t, "3.0.5", parsed.BundledTarantool)
	assert.Equal(t, "3.2.0", parsed.BundledTt)
	assert.Equal(t, "1.5.2", parsed.BundledTcm)

	// The build's lock is untouched: bundled_* exist only inside the archive,
	// never in the project's app.manifest.lock on disk.
	assert.Empty(t, lock.BundledTarantool)
	assert.Empty(t, lock.BundledTt)
	assert.Empty(t, lock.BundledTcm)
}

// TestLockWithBundledWithoutDeps pins the --without-deps contract: empty
// bundled_* are what tell an installer the archive carries no runtime.
func TestLockWithBundledWithoutDeps(t *testing.T) {
	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
	}

	out, err := lockWithBundled(lock, BundledVersions{})
	require.NoError(t, err)

	parsed, err := manifest.ParseLock(out)
	require.NoError(t, err)

	assert.Empty(t, parsed.BundledTarantool)
	assert.Empty(t, parsed.BundledTt)
	assert.Empty(t, parsed.BundledTcm)
}

// TestLockWithBundledPreservesClosure guards the nested [lock.products.<name>]
// round-trip: stamping bundled_* must not drop the resolved dependency set.
func TestLockWithBundledPreservesClosure(t *testing.T) {
	lock := &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		Products: map[string]manifest.LockProduct{
			"default": {Dependencies: []manifest.LockDependency{
				{Name: "inspect", Version: "3.1.3", Source: "registry", Checksum: "md5:abc"},
			}},
		},
	}

	out, err := lockWithBundled(lock, BundledVersions{Tarantool: "3.0.5"})
	require.NoError(t, err)

	parsed, err := manifest.ParseLock(out)
	require.NoError(t, err)

	require.Contains(t, parsed.Products, "default")
	require.Len(t, parsed.Products["default"].Dependencies, 1)
	assert.Equal(t, "inspect", parsed.Products["default"].Dependencies[0].Name)
	assert.Equal(t, "3.0.5", parsed.BundledTarantool)
}

func TestLockWithBundledRejectsNilLock(t *testing.T) {
	_, err := lockWithBundled(nil, BundledVersions{})
	require.Error(t, err)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
}

func TestPackageNamespaces(t *testing.T) {
	parse := func(t *testing.T, toml string) *manifest.Manifest {
		t.Helper()

		man, _, err := manifest.ParseManifest([]byte(toml))
		require.NoError(t, err)

		return man
	}

	base := `manifest_version = "0.1"

[package]
name = "my-app"

[platform]
tarantool = ">=3.0.0,<4.0.0"
tt = ">=2.0.0,<3.0.0"
`

	t.Run("default namespace is the package name", func(t *testing.T) {
		man := parse(t, base+`
[components.main]
path = "."

[products.default]
components = ["main"]
default = true
`)
		ns, flat := packageNamespaces(man, "default")
		assert.Equal(t, []string{"my-app"}, ns)
		assert.False(t, flat)
	})

	t.Run("explicit namespace is owned too", func(t *testing.T) {
		man := parse(t, base+`
[components.main]
path = "."

[components.roles]
path = "roles"
namespace = "app"

[products.default]
components = ["main", "roles"]
default = true
`)
		ns, flat := packageNamespaces(man, "default")
		assert.ElementsMatch(t, []string{"my-app", "app"}, ns)
		assert.False(t, flat)
	})

	t.Run("unknown product still owns the package name", func(t *testing.T) {
		man := parse(t, base+`
[components.main]
path = "."

[products.default]
components = ["main"]
default = true
`)
		ns, flat := packageNamespaces(man, "nope")
		assert.Equal(t, []string{"my-app"}, ns)
		assert.False(t, flat)
	})
}

// nativeManifest is a dependency-free pure-Lua package whose payload carries
// whatever bin/ holds: it packs with no compiler, registry or tarantool.
const nativeManifest = `manifest_version = '0.1'

[package]
name = 'native-app'
include = ['bin']

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=3.1.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
include = ['*.lua']
`

// packWithoutDeps runs the real pack, --without-deps, on a project made of
// nativeManifest and files, and returns the output directory alongside.
func packWithoutDeps(t *testing.T, files map[string]string) (*Result, string, error) {
	t.Helper()

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName: nativeManifest,
		versionFileName:  "1.0.0\n",
		"init.lua":       "return 1\n",
	})
	writeTree(t, src, files)

	out := filepath.Join(src, "_out")

	result, err := Run(context.Background(), Options{
		ProjectDir:  src,
		WithoutDeps: true,
		OutputDir:   out,
		Build: build.Options{
			TtVersion: "tt test",
			Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
		},
	})

	return result, out, err
}

// TestPackWithoutDepsNativeIncludeTakesHostToken: an executable from
// [package].include pins a --without-deps archive to the host platform just as
// a compiled module does, whatever its name.
func TestPackWithoutDepsNativeIncludeTakesHostToken(t *testing.T) {
	t.Parallel()

	result, _, err := packWithoutDeps(t, map[string]string{
		"bin/helper": string(hostNative(t)),
		"bin/run.sh": "#!/bin/sh\nexec tarantool\n",
	})
	require.NoError(t, err)

	assert.Equal(t, hostToken(), result.Token)
	assert.Equal(t, "native-app-1.0.0-"+hostToken()+archiveExt, filepath.Base(result.Path))
	assert.Contains(t, readArchive(t, result.Path), "bin/helper")
}

// TestPackWithoutDepsPureLuaIsUniversal: scripts and Lua are no native files,
// so such an archive stays "any".
func TestPackWithoutDepsPureLuaIsUniversal(t *testing.T) {
	t.Parallel()

	result, _, err := packWithoutDeps(t, map[string]string{
		"bin/run.sh": "#!/bin/sh\nexec tarantool\n",
	})
	require.NoError(t, err)

	assert.Equal(t, anyToken, result.Token)
	assert.Equal(t, "native-app-1.0.0-"+anyToken+archiveExt, filepath.Base(result.Path))
}

// TestPackRejectsForeignNative: a native file the host-labeled archive could
// not run fails the pack, naming it, and no archive is written.
func TestPackRejectsForeignNative(t *testing.T) {
	t.Parallel()

	foreign, foreignPlatform := foreignNative(t)

	_, out, err := packWithoutDeps(t, map[string]string{
		"bin/helper": string(foreign),
	})
	require.ErrorIs(t, err, errForeignNative)
	assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	assert.Contains(t, err.Error(), "bin/helper")
	assert.Contains(t, err.Error(), "runs on "+foreignPlatform.String())
	assert.Contains(t, err.Error(), "packed for "+hostToken())

	assert.NoDirExists(t, out)
}

// executableManifest is a package with a Lua component and an executable one,
// whose shell build copies the program named by $HELPER into TT_OUTPUT_DIR,
// and a second product without the executable component.
const executableManifest = `manifest_version = '0.1'

[package]
name = 'exec-app'

[platform]
tarantool = '>=3.0.0'
tt = '>=3.1.0'

[components.lua]
path = '.'
include = ['*.lua']

[components.helper]
path = 'helper'
kind = 'executable'

[components.helper.build]
backend = 'shell'
command = 'sh'
args = ['-c', 'cp "$HELPER" "$TT_OUTPUT_DIR/helper"']
env = { HELPER = '%s' }

[products.default]
components = ['lua', 'helper']
default = true

[products.lua-only]
components = ['lua']
`

// TestPackWithoutDepsCarriesOwnPrograms: the programs an executable component
// built travel in a --without-deps archive, labeled for the host, and the
// console script a rock left in .rocks/bin does not; a product without the
// component carries none of its programs.
func TestPackWithoutDepsCarriesOwnPrograms(t *testing.T) {
	t.Parallel()

	helper, err := os.Executable()
	require.NoError(t, err)

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName:   fmt.Sprintf(executableManifest, helper),
		versionFileName:    "1.0.0\n",
		"init.lua":         "return 1\n",
		"helper/README":    "the helper",
		".rocks/bin/tool":  "#!/bin/sh\n",
		".rocks/bin/other": "#!/bin/sh\n",
	})

	pack := func(product string) (*Result, error) {
		return Run(context.Background(), Options{
			ProjectDir:  src,
			Product:     product,
			WithoutDeps: true,
			OutputDir:   filepath.Join(src, "_out", product),
			Build: build.Options{
				TtVersion: "tt test",
				Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
			},
		})
	}

	result, err := pack("")
	require.NoError(t, err)

	entries := readArchive(t, result.Path)
	assert.Contains(t, entries, ".rocks/bin/helper")
	assert.NotContains(t, entries, ".rocks/bin/tool")
	assert.NotContains(t, entries, ".rocks/bin/other")
	assert.Contains(t, entries, ".rocks/share/tarantool/exec-app/init.lua")
	assert.NotContains(t, entries, ".rocks/share/tarantool/exec-app/README")
	assert.Equal(t, hostToken(), result.Token)

	result, err = pack("lua-only")
	require.NoError(t, err)
	assert.NotContains(t, readArchive(t, result.Path), ".rocks/bin/helper")
	assert.Equal(t, anyToken, result.Token)
}

// TestOutputDir pins where the archive lands: _build/pack under the project,
// unless an explicit output directory overrides it.
func TestOutputDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/proj", "_build", "pack"),
		outputDir(Options{ProjectDir: "/proj"}))
	assert.Equal(t, "/elsewhere",
		outputDir(Options{ProjectDir: "/proj", OutputDir: "/elsewhere"}))
}
