package pack

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
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
	assert.Equal(t, exitStateError, ExitCode(err))
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

func TestHasNativeArtifacts(t *testing.T) {
	t.Run("pure lua tree is universal", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{
			".rocks/share/tarantool/my-app/init.lua": "return 1",
			"README.md":                              "readme",
		})

		native, err := hasNativeArtifacts(dir)
		require.NoError(t, err)
		assert.False(t, native)
	})

	t.Run("a shared object pins the platform", func(t *testing.T) {
		dir := t.TempDir()
		writeTree(t, dir, map[string]string{
			".rocks/lib/tarantool/my-app/fast.so": "native",
		})

		native, err := hasNativeArtifacts(dir)
		require.NoError(t, err)
		assert.True(t, native)
	})
}

// TestOutputDir pins where the archive lands: RFC 0010 puts it in _build/pack.
func TestOutputDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/proj", "_build", "pack"),
		outputDir(Options{ProjectDir: "/proj"}))
	assert.Equal(t, "/elsewhere",
		outputDir(Options{ProjectDir: "/proj", OutputDir: "/elsewhere"}))
}
