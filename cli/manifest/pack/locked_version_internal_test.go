package pack

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// TestLockedTarantoolVersion pins decision 26: a Community build of a release
// tag is locked as the release, the name tt binary install gives it; a build
// off a tag, an Enterprise build and one of an undetermined flavor keep the
// version they report.
func TestLockedTarantoolVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		flavor  string
		want    string
	}{
		{"release ce", "3.8.1-0-ge643f88a5e0c", flavorCE, "3.8.1"},
		{"release ce, short hash", "3.2.0-0-g19607a903", flavorCE, "3.2.0"},
		{"bare release ce", "3.8.1", flavorCE, "3.8.1"},
		{"ce after the tag", "3.8.1-5-gabc1234", flavorCE, "3.8.1-5-gabc1234"},
		{"ce entrypoint", "3.8.0-entrypoint-49-g97a3b38040", flavorCE,
			"3.8.0-entrypoint-49-g97a3b38040"},
		{"ce dirty", "3.8.1-0-ge643f88a5e0c-dirty", flavorCE, "3.8.1-0-ge643f88a5e0c-dirty"},
		{"release ee", "3.8.1-0-ge643f88a5e0c", flavorEE, "3.8.1-0-ge643f88a5e0c"},
		{"ee revision", "3.2.1-0-g1a2b3c4-r579", flavorEE, "3.2.1-0-g1a2b3c4-r579"},
		{"undetermined flavor", "3.8.1-0-ge643f88a5e0c", "", "3.8.1-0-ge643f88a5e0c"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src := runtimeSource{Name: runtimeTarantool, Version: test.version, Flavor: test.flavor}
			assert.Equal(t, test.want, lockedTarantoolVersion(src))
		})
	}
}

// bundleActiveTarantool bundles the active tarantool of the given version and
// flavor, with no cache, and returns the versions the lock would record.
func bundleActiveTarantool(
	t *testing.T, ver, flavor string, tnt manifest.Constraint,
) BundledVersions {
	t.Helper()

	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "BSD-2-Clause",
		"bin/tt":        "#!/bin/sh\n",
	})

	bundled, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: tnt,
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: ver,
		ActiveTarantoolFlavor:  flavor,
		ActiveTt:               filepath.Join(prefix, "bin", "tt"),
		ActiveTtVersion:        "2.4.0",
	})
	require.NoError(t, err)

	return bundled
}

// TestBundleRuntimeLocksCEReleaseAsRelease: what pack stamps into the lock for
// a release Community tarantool is the release.
func TestBundleRuntimeLocksCEReleaseAsRelease(t *testing.T) {
	bundled := bundleActiveTarantool(t, "3.8.1-0-ge643f88a5e0c", flavorCE,
		constraint(">=3.0.0,<4.0.0"))
	assert.Equal(t, "3.8.1", bundled.Tarantool)
}

// TestBundleRuntimeKeepsCEOffTag: a Community build off a tag is no release
// and keeps its full version.
func TestBundleRuntimeKeepsCEOffTag(t *testing.T) {
	bundled := bundleActiveTarantool(t, "3.8.1-5-gabc1234", flavorCE,
		constraint(">=3.0.0,<4.0.0"))
	assert.Equal(t, "3.8.1-5-gabc1234", bundled.Tarantool)
}

// TestBundleRuntimeKeepsEE: an Enterprise version is not normalized, even of
// the release shape.
func TestBundleRuntimeKeepsEE(t *testing.T) {
	bundled := bundleActiveTarantool(t, "3.8.1-0-ge643f88a5e0c", flavorEE,
		flavored(">=3.0.0,<4.0.0", flavorEE))
	assert.Equal(t, "3.8.1-0-ge643f88a5e0c", bundled.Tarantool)
}

// TestBundleRuntimeLocksCachedCERelease: a release build taken from the ce
// tree of the cache is locked as the release too.
func TestBundleRuntimeLocksCachedCERelease(t *testing.T) {
	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.8.1-0-ge643f88a5e0c"},
		runtimeTt:        {"2.4.0"},
	})

	bundled, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "3.8.1", bundled.Tarantool)
	assert.Equal(t, "2.4.0", bundled.Tt)
}
