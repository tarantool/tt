package pack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

func constraint(spec string) manifest.Constraint {
	return manifest.Constraint{Version: spec}
}

// fakeCache builds a runtime cache with the given component versions, all under
// the ce flavor. Every tarantool entry gets a LICENSE, since bundling one
// without it is refused.
func fakeCache(t *testing.T, entries map[string][]string) string {
	t.Helper()

	return fakeFlavorCache(t, flavorCE, entries)
}

// fakeFlavorCache builds a runtime cache under one explicit flavor.
func fakeFlavorCache(t *testing.T, flavor string, entries map[string][]string) string {
	t.Helper()

	cache := t.TempDir()

	for component, versions := range entries {
		for _, ver := range versions {
			dir := filepath.Join(cache, component, flavor, ver)
			files := map[string]string{
				filepath.Join("bin", component): "#!/bin/sh\n",
			}

			if component == runtimeTarantool {
				files["LICENSE"] = "BSD-2-Clause"
			}

			writeTree(t, dir, files)
		}
	}

	return cache
}

// TestSatisfies uses the manifest's documented constraint syntax - a
// comma-separated hashicorp range, as in testdata/my-app.toml's
// ">=3.0.0,<4.0.0[ce]". Caret ranges are npm/cargo syntax and are rejected.
func TestSatisfies(t *testing.T) {
	tests := []struct {
		name string
		ver  string
		spec string
		want bool
	}{
		{"range matches", "3.2.0", ">=3.0.0,<4.0.0", true},
		{"range rejects major bump", "4.0.0", ">=3.0.0,<4.0.0", false},
		{"range rejects below floor", "2.11.0", ">=3.0.0,<4.0.0", false},
		{"lower bound only", "3.1.0", ">=3.1.0", true},
		{"empty constraint accepts anything", "3.0.5", "", true},
		{"empty version never satisfies", "", ">=3.0.0", false},
		{"leading v is tolerated", "v3.0.5", ">=3.0.0,<4.0.0", true},
		{"exact pin", "3.0.5", "3.0.5", true},
		{"unparseable version is a miss", "not-a-version", ">=3.0.0", false},
		// Every Tarantool development build reports a prerelease suffix; under
		// strict semver it would satisfy no ordinary range at all.
		{"tarantool entrypoint build", "3.8.0-entrypoint-49-g97a3b38040",
			">=3.0.0,<4.0.0", true},
		{"prerelease still respects bounds", "4.0.0-entrypoint-1-gabc",
			">=3.0.0,<4.0.0", false},
		{"build metadata is dropped", "3.2.0+deadbeef", ">=3.0.0,<4.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := satisfies(tt.ver, constraint(tt.spec))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestSatisfiesRejectsBadConstraint pins the no-silent-fallback rule: a typo in
// [platform] must fail loudly rather than degrade to an exact-string match and
// bundle the wrong runtime.
func TestSatisfiesRejectsBadConstraint(t *testing.T) {
	for _, spec := range []string{"^3.0", "~3.0.0.0", "not a constraint"} {
		_, err := satisfies("3.0.5", constraint(spec))
		require.Error(t, err, "constraint %q must be rejected", spec)
		assert.ErrorIs(t, err, errBadConstraint)
	}
}

// TestFindInCachePicksHighest is what makes the cache deterministic: with
// several satisfying versions present, the highest wins, not the first read.
func TestFindInCachePicksHighest(t *testing.T) {
	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.0.1", "3.2.0", "3.10.0", "2.11.0", "4.0.0"},
	})

	dir, ver, ok, err := findInCache(cache, runtimeTarantool, flavorCE, constraint(">=3.0.0,<4.0.0"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "3.10.0", ver, "3.10.0 > 3.2.0 numerically, not lexically")
	assert.Equal(t, filepath.Join(cache, runtimeTarantool, flavorCE, "3.10.0"), dir)
}

func TestFindInCacheMisses(t *testing.T) {
	cache := fakeCache(t, map[string][]string{runtimeTarantool: {"2.11.0"}})

	_, _, ok, err := findInCache(cache, runtimeTarantool, flavorCE, constraint(">=3.0.0,<4.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "no satisfying version")

	_, _, ok, err = findInCache(cache, runtimeTt, flavorCE, constraint(">=2.0.0,<3.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "component absent from cache")

	_, _, ok, err = findInCache("", runtimeTarantool, flavorCE, constraint(">=3.0.0,<4.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "no cache configured")
}

func TestBundleRuntimeFromCache(t *testing.T) {
	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.0.5", "3.1.0"},
		runtimeTt:        {"2.5.0"},
	})
	stageDir := t.TempDir()

	var warnings []string

	bundled, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		Warn: func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, "3.1.0", bundled.Tarantool)
	assert.Equal(t, "2.5.0", bundled.Tt)
	assert.Empty(t, bundled.Tcm, "tcm unset in [platform] is not bundled")
	assert.Empty(t, warnings, "the cache path must not warn")

	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "bin", "tarantool"))
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "LICENSE"))
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tt", "bin", "tt"))
	assert.NoDirExists(t, filepath.Join(stageDir, "_runtime", "tcm"))
}

func TestBundleRuntimeIncludesTcmWhenDeclared(t *testing.T) {
	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.0.5"},
		runtimeTt:        {"2.5.0"},
		runtimeTcm:       {"1.5.2"},
	})
	stageDir := t.TempDir()

	bundled, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
			Tcm:       constraint(">=1.5.0,<2.0.0"),
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "1.5.2", bundled.Tcm)
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tcm", "bin", "tcm"))
}

// TestBundleRuntimeFallsBackToActive covers the v0 convenience path: with no
// runtime cache yet, the active binary is accepted when it satisfies the
// constraint — and the user is told the archive is not cache-reproducible.
func TestBundleRuntimeFallsBackToActive(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "BSD-2-Clause",
		"bin/tt":        "#!/bin/sh\n",
	})

	stageDir := t.TempDir()

	var warnings []string

	bundled, err := bundleRuntime(stageDir, RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "3.0.5",
		ActiveTt:               filepath.Join(prefix, "bin", "tt"),
		ActiveTtVersion:        "2.4.0",
		Warn:                   func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, "3.0.5", bundled.Tarantool)
	assert.Equal(t, "2.4.0", bundled.Tt)
	assert.Len(t, warnings, 2, "each fallback must warn")

	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "bin", "tarantool"))
	// The license is picked up from beside the binary's install prefix.
	assert.FileExists(t, filepath.Join(stageDir, "_runtime", "tarantool", "LICENSE"))
}

// TestBundleRuntimeRejectsEmptyPlatform is a regression test. The [platform]
// constraints reach bundleRuntime from the built manifest; when nothing filled
// them, every component was skipped as "unset" and a with-deps pack produced an
// archive with no _runtime/ at all - silently, and byte-comparable to
// --without-deps. An empty platform must fail loudly instead.
func TestBundleRuntimeRejectsEmptyPlatform(t *testing.T) {
	tests := []struct {
		name     string
		platform manifest.Platform
	}{
		{"nothing set", manifest.Platform{}},
		{"tarantool only", manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
		}},
		{"tt only", manifest.Platform{Tt: constraint(">=2.0.0,<3.0.0")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stageDir := t.TempDir()

			_, err := bundleRuntime(stageDir, RuntimeOptions{
				CacheDir: fakeCache(t, map[string][]string{
					runtimeTarantool: {"3.0.5"},
					runtimeTt:        {"2.5.0"},
				}),
				Platform: tt.platform,
			})

			require.Error(t, err)
			assert.ErrorIs(t, err, errNoRuntime)
			assert.NoDirExists(t, filepath.Join(stageDir, "_runtime"))
		})
	}
}

// TestBundleRuntimeRejectsUnsatisfiedActive guards against silently bundling
// the wrong Tarantool: a fallback that misses the constraint is an error, not
// a warning.
func TestBundleRuntimeRejectsUnsatisfiedActive(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{"bin/tarantool": "#!/bin/sh\n"})

	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "2.11.0",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errNoRuntime)
	assert.Equal(t, exitStateError, ExitCode(err))
}

func TestBundleRuntimeNoSourceAtAll(t *testing.T) {
	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errNoRuntime)
}

// TestBundleRuntimeRequiresTarantoolLicense enforces the RFC rule that a
// bundled Tarantool ships its LICENSE.
func TestBundleRuntimeRequiresTarantoolLicense(t *testing.T) {
	cache := t.TempDir()
	writeTree(t, filepath.Join(cache, runtimeTarantool, flavorCE, "3.0.5"), map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
	})

	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: cache,
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errNoTarantoolLicense)
}

func TestPlaceRuntimeKeepsExecutableBit(t *testing.T) {
	src := t.TempDir()
	binPath := filepath.Join(src, "bin", "tt")
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), dirPerm))
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755))

	stageDir := t.TempDir()
	require.NoError(t, placeRuntime(stageDir, runtimeSource{
		Name: runtimeTt, Version: "2.0.0", Dir: src,
	}))

	info, err := os.Stat(filepath.Join(stageDir, "_runtime", "tt", "bin", "tt"))
	require.NoError(t, err)
	assert.NotZero(t, info.Mode().Perm()&0o100, "the binary must stay executable")
}
