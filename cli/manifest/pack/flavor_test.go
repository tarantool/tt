package pack

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// flavored builds a constraint with an explicit flavor.
func flavored(spec, flavor string) manifest.Constraint {
	return manifest.Constraint{Version: spec, Flavor: flavor}
}

func TestFlavorFromBanner(t *testing.T) {
	tests := []struct {
		name   string
		banner string
		want   string
	}{
		{"community", "Tarantool 3.2.0-0-g19607a903\nTarget: Darwin\n", flavorCE},
		{"enterprise", "Tarantool Enterprise 3.2.0-0-g19607a903\nTarget: Linux\n", flavorEE},
		{"entrypoint community", "Tarantool 3.8.0-entrypoint-49-g97a3b38040\n", flavorCE},
		{"unrecognized banner", "weird\n", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, flavorFromBanner(tt.banner))
		})
	}
}

// TestFindInCacheSeparatesFlavors pins the cache layout: findInCache searches
// exactly one flavor tree, so the two flavors of one version can coexist.
// Which trees a requirement may draw from is resolveRuntime's decision.
func TestFindInCacheSeparatesFlavors(t *testing.T) {
	cache := fakeFlavorCache(t, flavorEE, map[string][]string{
		runtimeTarantool: {"3.0.5"},
	})

	_, _, ok, err := findInCache(cache, runtimeTarantool, flavorCE, constraint(">=3.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "the ce tree is empty, so a ce lookup misses")

	dir, ver, ok, err := findInCache(cache, runtimeTarantool, flavorEE, constraint(">=3.0.0"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "3.0.5", ver)
	assert.Equal(t, filepath.Join(cache, runtimeTarantool, flavorEE, "3.0.5"), dir)
}

// TestResolveRuntimeCEAcceptsEETree: Enterprise is a superset of Community, so
// a [ce] requirement is satisfied by a cached EE build when no CE one is.
func TestResolveRuntimeCEAcceptsEETree(t *testing.T) {
	cache := fakeFlavorCache(t, flavorEE, map[string][]string{
		runtimeTarantool: {"3.0.5"},
	})

	src, err := resolveRuntime(RuntimeOptions{CacheDir: cache}, runtimeTarantool,
		constraint(">=3.0.0"), activeBinary{})
	require.NoError(t, err)
	assert.Equal(t, "3.0.5", src.Version)
	assert.Equal(t, filepath.Join(cache, runtimeTarantool, flavorEE, "3.0.5"), src.Dir)
	assert.False(t, src.Fallback)
}

// TestResolveRuntimeCEPrefersCETree: with both trees populated, a [ce]
// requirement takes the CE build even when the EE tree holds a higher version.
// The requirement's own flavor is what the project asked for; EE is a
// stand-in, not an upgrade.
func TestResolveRuntimeCEPrefersCETree(t *testing.T) {
	cache := fakeFlavorCache(t, flavorCE, map[string][]string{
		runtimeTarantool: {"3.0.5"},
	})
	writeTree(t, filepath.Join(cache, runtimeTarantool, flavorEE, "3.9.0"), map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "Tarantool Enterprise",
	})

	src, err := resolveRuntime(RuntimeOptions{CacheDir: cache}, runtimeTarantool,
		constraint(">=3.0.0"), activeBinary{})
	require.NoError(t, err)
	assert.Equal(t, "3.0.5", src.Version)
	assert.Equal(t, filepath.Join(cache, runtimeTarantool, flavorCE, "3.0.5"), src.Dir)
}

// TestResolveRuntimeEERejectsCETree is the other direction, which stays
// strict: an [ee] requirement never resolves to a CE build.
func TestResolveRuntimeEERejectsCETree(t *testing.T) {
	cache := fakeFlavorCache(t, flavorCE, map[string][]string{
		runtimeTarantool: {"3.0.5"},
	})

	_, err := resolveRuntime(RuntimeOptions{CacheDir: cache}, runtimeTarantool,
		flavored(">=3.0.0", flavorEE), activeBinary{})
	require.Error(t, err)
	assert.ErrorIs(t, err, errNoRuntime)
}

// TestBundleRuntimeCEAcceptsEEFallback covers the SDK case: a manifest with no
// flavor (the [ce] default) packed in an Enterprise environment bundles the
// active EE Tarantool instead of refusing.
func TestBundleRuntimeCEAcceptsEEFallback(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "Tarantool Enterprise",
		"bin/tt":        "#!/bin/sh\n",
	})

	bundled, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "3.5.0-0-g0823718c2",
		ActiveTarantoolFlavor:  flavorEE,
		ActiveTt:               filepath.Join(prefix, "bin", "tt"),
		ActiveTtVersion:        "2.4.0",
	})

	require.NoError(t, err)
	assert.Equal(t, "3.5.0-0-g0823718c2", bundled.Tarantool)
}

// TestBundleRuntimeRejectsWrongFlavorFallback is the regression test for the
// original defect: an [ee] manifest silently bundled the active CE Tarantool.
func TestBundleRuntimeRejectsWrongFlavorFallback(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "BSD-2-Clause",
	})

	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: flavored(">=3.0.0,<4.0.0", flavorEE),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "3.0.5",
		ActiveTarantoolFlavor:  flavorCE,
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errNoRuntime)
	assert.Contains(t, err.Error(), "[ee] requires a [ee] build",
		"the error must name the wanted flavor")
	assert.Contains(t, err.Error(), "matches the version but not the flavor",
		"a version that fits must not be reported as a version mismatch")
	assert.Contains(t, err.Error(), "3.0.5[ce]", "the error must describe the active build")
}

// TestBundleRuntimeFallbackSDKLayout covers the Enterprise SDK layout, where
// tarantool sits at the root of the unpacked bundle beside env.sh with no bin/
// level: a license and a share/ tree there must be found next to the binary,
// not one directory above the bundle.
//
// The bundle here carries a LICENSE, which a released SDK does not - see
// TestBundleRuntimeFallbackSDKWithoutLicense for that shape. What this pins is
// the search path, which is also what a hand-populated cache entry relies on.
func TestBundleRuntimeFallbackSDKLayout(t *testing.T) {
	sdk := filepath.Join(t.TempDir(), "te350")
	writeTree(t, sdk, map[string]string{
		"tarantool":             "#!/bin/sh\n",
		"LICENSE":               "Tarantool Enterprise",
		"env.sh":                "export PATH=$PATH\n",
		"share/tarantool/x.lua": "return 1\n",
		"bin/tt":                "#!/bin/sh\n",
	})

	stage := t.TempDir()

	bundled, err := bundleRuntime(stage, RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(sdk, "tarantool"),
		ActiveTarantoolVersion: "3.5.0-0-g0823718c2",
		ActiveTarantoolFlavor:  flavorEE,
		ActiveTt:               filepath.Join(sdk, "bin", "tt"),
		ActiveTtVersion:        "2.4.0",
	})

	require.NoError(t, err)
	assert.Equal(t, "3.5.0-0-g0823718c2", bundled.Tarantool)

	tnt := filepath.Join(stage, runtimeDirName, runtimeTarantool)
	assert.FileExists(t, filepath.Join(tnt, "LICENSE"))
	assert.FileExists(t, filepath.Join(tnt, "share", "tarantool", "x.lua"),
		"the SDK's share/ tree lives beside the binary and must come along")
}

// TestBundleRuntimeFallbackSDKWithoutLicense covers the Enterprise SDK as it
// is actually shipped: no LICENSE, no COPYING and no share/tarantool anywhere
// in the bundle or in the tarball it comes from. Requiring a license here would
// make an EE runtime unpackable, so the pack goes through and says what it
// could not find.
func TestBundleRuntimeFallbackSDKWithoutLicense(t *testing.T) {
	sdk := filepath.Join(t.TempDir(), "3.7.0-r137")
	writeTree(t, sdk, map[string]string{
		"tarantool":   "#!/bin/sh\n",
		"tt":          "#!/bin/sh\n",
		"tcm":         "#!/bin/sh\n",
		"env.sh":      "export PATH=$PATH\n",
		"VERSION":     "TARANTOOL_EE=3.7.0-0-g1f1ec9fdf\n",
		"README.md":   "# Tarantool Enterprise\n",
		"include/x.h": "\n",
	})

	var warnings []string

	stage := t.TempDir()

	bundled, err := bundleRuntime(stage, RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(sdk, "tarantool"),
		ActiveTarantoolVersion: "3.7.0-0-g1f1ec9fdf",
		ActiveTarantoolFlavor:  flavorEE,
		ActiveTt:               filepath.Join(sdk, "tt"),
		ActiveTtVersion:        "2.4.0",
		Warn:                   func(msg string) { warnings = append(warnings, msg) },
	})

	require.NoError(t, err)
	assert.Equal(t, "3.7.0-0-g1f1ec9fdf", bundled.Tarantool)

	tnt := filepath.Join(stage, runtimeDirName, runtimeTarantool)
	assert.FileExists(t, filepath.Join(tnt, "bin", runtimeTarantool))
	assert.NoFileExists(t, filepath.Join(tnt, "LICENSE"))

	var licenseWarnings []string

	for _, w := range warnings {
		if strings.Contains(w, "without a license") {
			licenseWarnings = append(licenseWarnings, w)
		}
	}

	require.Len(t, licenseWarnings, 1)
	assert.Contains(t, licenseWarnings[0], sdk,
		"the warning must name where the license was looked for")
}

// TestBundleRuntimeFallbackUsesResolvedPrefix: when tt already knows the
// install prefix (TT_CLI_TARANTOOL_PREFIX, the build banner), that is where
// the license and share/ are looked for first, ahead of any guess from the
// binary's location.
func TestBundleRuntimeFallbackUsesResolvedPrefix(t *testing.T) {
	bin := t.TempDir()
	writeTree(t, bin, map[string]string{"tarantool": "#!/bin/sh\n", "tt": "#!/bin/sh\n"})

	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{"LICENSE": "BSD-2-Clause"})

	stage := t.TempDir()

	_, err := bundleRuntime(stage, RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(bin, "tarantool"),
		ActiveTarantoolVersion: "3.0.5",
		ActiveTarantoolPrefix:  prefix,
		ActiveTt:               filepath.Join(bin, "tt"),
		ActiveTtVersion:        "2.4.0",
	})

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(stage, runtimeDirName, runtimeTarantool, "LICENSE"))
}

// TestBundleRuntimeVersionMismatchIsNotAFlavorMismatch keeps the two failure
// texts apart: a version that does not fit is reported as such, without the
// flavor hint that would send the user to edit the wrong field.
func TestBundleRuntimeVersionMismatchIsNotAFlavorMismatch(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "BSD-2-Clause",
	})

	_, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: constraint(">=3.0.0,<4.0.0"),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "2.11.0",
		ActiveTarantoolFlavor:  flavorCE,
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errNoRuntime)
	assert.Contains(t, err.Error(), "does not satisfy it")
	assert.NotContains(t, err.Error(), "not the flavor")
}

// TestBundleRuntimeAcceptsMatchingFlavorFallback is the positive counterpart.
func TestBundleRuntimeAcceptsMatchingFlavorFallback(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "Tarantool Enterprise",
		"bin/tt":        "#!/bin/sh\n",
	})

	bundled, err := bundleRuntime(t.TempDir(), RuntimeOptions{
		CacheDir: filepath.Join(t.TempDir(), "empty"),
		Platform: manifest.Platform{
			Tarantool: flavored(">=3.0.0,<4.0.0", flavorEE),
			Tt:        constraint(">=2.0.0,<3.0.0"),
		},
		ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
		ActiveTarantoolVersion: "3.0.5",
		ActiveTarantoolFlavor:  flavorEE,
		ActiveTt:               filepath.Join(prefix, "bin", "tt"),
		ActiveTtVersion:        "2.4.0",
	})

	require.NoError(t, err)
	assert.Equal(t, "3.0.5", bundled.Tarantool)
}

// TestBundleRuntimeUndeterminedFlavorOnlySatisfiesCE covers the safety rule for
// a binary whose flavor could not be probed: usable for the [ce] default,
// refused for [ee], where guessing would be a licensing error.
func TestBundleRuntimeUndeterminedFlavorOnlySatisfiesCE(t *testing.T) {
	prefix := t.TempDir()
	writeTree(t, prefix, map[string]string{
		"bin/tarantool": "#!/bin/sh\n",
		"LICENSE":       "BSD-2-Clause",
		"bin/tt":        "#!/bin/sh\n",
	})

	opts := func(flavor string) RuntimeOptions {
		return RuntimeOptions{
			CacheDir: filepath.Join(t.TempDir(), "empty"),
			Platform: manifest.Platform{
				Tarantool: flavored(">=3.0.0,<4.0.0", flavor),
				Tt:        constraint(">=2.0.0,<3.0.0"),
			},
			ActiveTarantool:        filepath.Join(prefix, "bin", "tarantool"),
			ActiveTarantoolVersion: "3.0.5",
			ActiveTarantoolFlavor:  "", // Undetermined.
			ActiveTt:               filepath.Join(prefix, "bin", "tt"),
			ActiveTtVersion:        "2.4.0",
		}
	}

	_, err := bundleRuntime(t.TempDir(), opts(flavorCE))
	require.NoError(t, err, "an undetermined flavor stands in for the ce default")

	_, err = bundleRuntime(t.TempDir(), opts(flavorEE))
	require.Error(t, err, "an undetermined flavor must never pass for ee")
	assert.ErrorIs(t, err, errNoRuntime)
}

// TestSatisfiesFlavorOnlyConstraintNeedsAVersion closes the hole where a
// constraint carrying only a flavor matched anything, including a binary whose
// version was unknown - which then stamped an empty bundled_*_version.
func TestSatisfiesFlavorOnlyConstraintNeedsAVersion(t *testing.T) {
	flavorOnly := flavored("", flavorEE)

	require.False(t, flavorOnly.IsZero(), "a flavor-only constraint is not zero")

	got, err := satisfies("", flavorOnly)
	require.NoError(t, err)
	assert.False(t, got, "an undetermined version must satisfy nothing")

	got, err = satisfies("3.0.5", flavorOnly)
	require.NoError(t, err)
	assert.True(t, got, "a known version still matches an unbounded range")
}
