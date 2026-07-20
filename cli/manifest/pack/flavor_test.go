package pack

import (
	"path/filepath"
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

// TestFindInCacheSeparatesFlavors is the core of the flavor fix: a [ce]
// requirement must never resolve to an EE tree, and vice versa. Before the
// cache grew a flavor level the two were indistinguishable.
func TestFindInCacheSeparatesFlavors(t *testing.T) {
	cache := fakeFlavorCache(t, flavorEE, map[string][]string{
		runtimeTarantool: {"3.0.5"},
	})

	_, _, ok, err := findInCache(cache, runtimeTarantool, flavorCE, constraint(">=3.0.0"))
	require.NoError(t, err)
	assert.False(t, ok, "a ce requirement must not resolve to the ee tree")

	dir, ver, ok, err := findInCache(cache, runtimeTarantool, flavorEE, constraint(">=3.0.0"))
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "3.0.5", ver)
	assert.Equal(t, filepath.Join(cache, runtimeTarantool, flavorEE, "3.0.5"), dir)
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
	assert.Contains(t, err.Error(), "[ee]", "the error must name the wanted flavor")
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
