package install

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
)

func eeTestBundles(t *testing.T, names ...string) search.BundleInfoSlice {
	t.Helper()

	bundles := make(search.BundleInfoSlice, 0, len(names))

	for _, name := range names {
		ver, err := version.Parse(name)
		require.NoError(t, err)

		ver.Tarball = "tarantool-enterprise-sdk-" + name + ".linux.x86_64.tar.gz"
		bundles = append(bundles, search.BundleInfo{
			Version: ver,
			Package: "enterprise",
			Release: "release-" + name,
			Token:   "token-" + name,
		})
	}

	sort.Sort(bundles)

	return bundles
}

func TestSelectEeBundleVersion(t *testing.T) {
	bundles := eeTestBundles(t,
		"gc64-2.11.9-0-r99",
		"gc64-2.11.9-0-r100",
		"nogc64-2.11.9-0-r101",
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
		{"other major", "2", "gc64-2.11.9-0-r100"},
		{"legacy gc64 identity", "gc64-2.11.9", "gc64-2.11.9-0-r100"},
		{"legacy nogc64 identity", "nogc64-2.11.9", "nogc64-2.11.9-0-r101"},
		{"rc identity", "3.8.1-rc2", "gc64-3.8.1-rc2-0-r30"},
		{"debug identity", "debug-3.9.2", "debug-gc64-3.9.2-0-r99"},
		{"exact older gc64", "gc64-3.4.0-0-r9", "gc64-3.4.0-0-r9"},
		{"exact nogc64", "nogc64-3.9.1-0-r90", "nogc64-3.9.1-0-r90"},
		{"exact debug", "debug-gc64-3.9.2-0-r99", "debug-gc64-3.9.2-0-r99"},
		{"exact rc", "gc64-3.8.1-rc2-0-r30", "gc64-3.8.1-rc2-0-r30"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle, err := selectBundleVersion(bundles, tt.requested, binary.ProgramEe)
			require.NoError(t, err)

			expected, err := search.SelectVersion(bundles, tt.expected)
			require.NoError(t, err)
			require.Equal(t, expected, bundle)
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
		{"other major", []string{"gc64-2.11.9-0-r99"}, "3"},
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
