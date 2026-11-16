package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// TestRuntimeSatisfies pins the rule pack bundles a runtime by and install
// checks it by: a prerelease meets a range by its core version.
func TestRuntimeSatisfies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ver, spec string
		want      bool
	}{
		{"3.0.0", ">=3.0.0", true},
		{"3.0.0-alpha.2", ">=3.0.0", true},
		{"3.0.0-alpha.2", ">=3.0.0-alpha", true},
		{"v3.0.0-alpha.2", ">=3.0.0,<4.0.0", true},
		{"3.8.0-entrypoint-49-g97a3b38040", ">=3.0.0,<4.0.0", true},
		{"3.8.1-0-ge643f88a5e0c", ">=3.8.1", true},
		{"2.11.5", ">=3.0.0", false},
		{"4.0.0-rc1", "<4.0.0", false},
		{"3.0.0", "", true},
		{"", "", false},
		{"  ", ">=3.0.0", false},
	}

	for _, tc := range cases {
		got, err := manifest.RuntimeSatisfies(tc.ver, tc.spec)
		require.NoError(t, err, "%s %s", tc.ver, tc.spec)
		assert.Equal(t, tc.want, got, "%s %s", tc.ver, tc.spec)
	}

	_, err := manifest.RuntimeSatisfies("3.0.0", ">=three")
	require.ErrorIs(t, err, manifest.ErrRuntimeConstraint)

	_, err = manifest.RuntimeSatisfies("nightly", ">=3.0.0")
	require.ErrorIs(t, err, manifest.ErrRuntimeVersion)
}

func TestIsRuntimePrerelease(t *testing.T) {
	t.Parallel()

	for ver, want := range map[string]bool{
		"3.0.0":                           false,
		"v3.0.0":                          false,
		"3.8.1-0-ge643f88a5e0c":           false,
		"3.0.0-alpha.2":                   true,
		"3.8.0-entrypoint-49-g97a3b38040": true,
		"3.8.1-3-ge643f88a5e0c":           true,
		"3.0.0+meta":                      true,
	} {
		assert.Equal(t, want, manifest.IsRuntimePrerelease(ver), ver)
	}
}

func TestRuntimeRelease(t *testing.T) {
	t.Parallel()

	release, ok := manifest.RuntimeRelease("3.8.1-0-ge643f88a5e0c")
	assert.True(t, ok)
	assert.Equal(t, "3.8.1", release)

	_, ok = manifest.RuntimeRelease("3.8.1-2-ge643f88a5e0c")
	assert.False(t, ok)

	_, ok = manifest.RuntimeRelease("3.0.0-alpha.2")
	assert.False(t, ok)
}
