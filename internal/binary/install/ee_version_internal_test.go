package install

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

// testBundles returns the builds of versions in the order ListBundles sorts
// them, with archives named by name.
func testBundles(t *testing.T, name func(string) string, versions ...string) binary.Bundles {
	t.Helper()

	bundles := make(binary.Bundles, 0, len(versions))

	for _, ver := range versions {
		parsed, err := version.Parse(ver)
		require.NoError(t, err)

		parsed.Tarball = name(ver)
		bundles = append(bundles, binary.Bundle{
			Source:   nil,
			Artifact: bundle.Artifact{Version: ver, Name: parsed.Tarball, Locator: "test:" + ver},
			Version:  parsed,
		})
	}

	slices.SortStableFunc(bundles, func(left, right binary.Bundle) int {
		switch {
		case binary.LessVersion(left.Version, right.Version):
			return -1
		case binary.LessVersion(right.Version, left.Version):
			return 1
		default:
			return 0
		}
	})

	return bundles
}

func eeTestBundles(t *testing.T, names ...string) binary.Bundles {
	t.Helper()

	return testBundles(t, func(ver string) string {
		return "tarantool-enterprise-sdk-" + ver + ".linux.x86_64.tar.gz"
	}, names...)
}

func TestSelectEeBundleVersion(t *testing.T) {
	bundles := eeTestBundles(t,
		"gc64-3.4.0-0-r9",
		"gc64-3.4.0-0-r60",
		"gc64-3.4.1-0-r10",
		"gc64-3.4.1-0-r20",
		"gc64-3.8.1-0-r3",
		"gc64-3.8.1-rc2-0-r30",
		"gc64-3.9.0-rc10-0-r40",
		"nogc64-3.9.1-0-r90",
		"debug-gc64-3.9.2-0-r99",
		"gc64-4.0.0-0-r1",
		"gc64-4.1.0-rc1-0-r2",
		"nogc64-4.2.0-0-r3",
		"debug-gc64-4.3.0-0-r4",
	)

	tests := []struct {
		name      string
		requested string
		expected  string
	}{
		{"latest", "", "gc64-4.0.0-0-r1"},
		{"major", "3", "gc64-3.8.1-0-r3"},
		{"minor", "3.4", "gc64-3.4.1-0-r20"},
		{"patch", "3.4.0", "gc64-3.4.0-0-r60"},
		{"stable before rc", "3.8.1", "gc64-3.8.1-0-r3"},
		{"other major", "4", "gc64-4.0.0-0-r1"},
		{"rc identity", "3.8.1-rc2", "gc64-3.8.1-rc2-0-r30"},
		{"debug identity", "debug-3.9.2", "debug-gc64-3.9.2-0-r99"},
		{"exact older gc64", "gc64-3.4.0-0-r9", "gc64-3.4.0-0-r9"},
		{"exact nogc64", "nogc64-3.9.1-0-r90", "nogc64-3.9.1-0-r90"},
		{"exact debug", "debug-gc64-3.9.2-0-r99", "debug-gc64-3.9.2-0-r99"},
		{"exact rc", "gc64-3.8.1-rc2-0-r30", "gc64-3.8.1-rc2-0-r30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selected, err := selectBundleVersion(bundles, tt.requested, binary.ProgramEe)
			require.NoError(t, err)

			expected, err := selectBundle(bundles, tt.expected)
			require.NoError(t, err)
			require.Equal(t, expected, selected)
		})
	}
}

func TestSelectEeBundleVersionRejectsPartialDecorations(t *testing.T) {
	bundles := eeTestBundles(t, "gc64-3.4.0-0-r60", "gc64-3.4.0-rc1-0-r60")

	for _, requested := range []string{
		"gc64-3", "gc64-3.4", "gc64-3.4.0",
		"gc64-3.4.0-rc1", "gc64-3.4.0-0", "3.4.0-0-r60",
		"gc64-3.4.0-0-r61", "v3", "3.4.0.1", "3.", "3..4", "latest",
	} {
		t.Run(requested, func(t *testing.T) {
			_, err := selectBundleVersion(bundles, requested, binary.ProgramEe)
			require.Error(t, err)
		})
	}
}

func TestSelectEeBundleVersionNoDefaultMatch(t *testing.T) {
	tests := []struct {
		name      string
		versions  []string
		requested string
	}{
		{"empty", nil, "3"},
		{"other major", []string{"gc64-4.0.0-0-r1"}, "3"},
		{"nogc64 only", []string{"nogc64-3.4.0-0-r60"}, "3"},
		{"debug only", []string{"debug-gc64-3.4.0-0-r60"}, "3"},
		{"rc only", []string{"gc64-3.4.0-rc1-0-r60"}, "3"},
		{"latest empty", nil, ""},
		{"latest nogc64 only", []string{"nogc64-3.4.0-0-r60"}, ""},
		{"latest debug only", []string{"debug-gc64-3.4.0-0-r60"}, ""},
		{"latest rc only", []string{"gc64-3.4.0-rc1-0-r60"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundles := eeTestBundles(t, tt.versions...)
			_, err := selectBundleVersion(bundles, tt.requested, binary.ProgramEe)
			require.ErrorIs(t, err, version.ErrNotFound)
		})
	}
}
