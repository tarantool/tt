package version_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
)

func parseVersions(t *testing.T, verStr []string) []version.Version {
	t.Helper()

	versions := make([]version.Version, 0, len(verStr))
	for _, v := range verStr {
		ver, err := version.Parse(v)
		require.NoError(t, err)

		versions = append(versions, ver)
	}

	return versions
}

func TestMatchVersion(t *testing.T) {
	sortedVersions := parseVersions(t, []string{
		"1.10.14",
		"1.10.15",
		"v2.8.1",
		"v2.8.2",
		"v2.10.0-beta1",
		"v2.10.0-beta2",
		"v2.10.0-rc1",
		"v2.10.0",
		"v2.10.2",
		"v2.10.3",
		"v2.10.4",
		"v2.10.5-entrypoint",
		"v2.10.5",
		"v2.10.6-entrypoint",
		"v2.10.6",
		"v2.10.7-entrypoint",
		"v2.10.7",
		"v2.10.8-entrypoint",
		"v2.10.8",
		"v2.10.9-entrypoint",
		"v2.11.0-entrypoint",
		"v2.11.0-rc1",
		"v2.11.0-rc2",
		"v2.11.0",
		"v2.11.1-entrypoint",
		"v2.11.1",
		"v2.11.2-entrypoint",
		"v2.11.2",
		"v2.11.3-entrypoint",
		"v2.11.3",
		"v2.11.4-entrypoint",
		"v2.11.4",
		"v2.11.5-entrypoint",
		"3.0.0-entrypoint",
		"3.0.0-alpha1",
		"3.0.0-alpha2",
		"3.0.0-alpha3",
		"3.0.0-beta1",
		"gc64-3.0.0-1-ga73da88-r123",
		"3.0.1",
		"3.0.2",
		"3.0.3-entrypoint",
		"3.1.0-entrypoint",
		"3.1.1-entrypoint",
		"3.1.2-entrypoint",
		"3.2.0-entrypoint",
		"3.2.0",
		"3.2.1-entrypoint",
		"3.3.0-entrypoint",
		"3.3.0",
	})
	data := []struct {
		find     string
		expected string
		errMsg   string
	}{
		{"1", "1.10.15", ""},
		{"2", "v2.11.4", ""},
		{"2.10.6", "v2.10.6", ""},
		{"v2.10", "v2.10.8", ""},
		{"2.11.0-rc1", "v2.11.0-rc1", ""},
		{"2.11-rc", "v2.11.0-rc2", ""},
		{"2.11", "v2.11.4", ""},
		{"3.0.0-alpha2", "3.0.0-alpha2", ""},
		{"3.003", "3.3.0", ""},
		{"gc64-3", "gc64-3.0.0-1-ga73da88-r123", ""},
		{"3.0-1", "gc64-3.0.0-1-ga73da88-r123", ""},
		{"3.0-r123", "gc64-3.0.0-1-ga73da88-r123", ""},
		{"v-ga73da88", "gc64-3.0.0-1-ga73da88-r123", ""},

		{"1234.5678.90", "", `not matched with: "1234.5678.90"`},
		{"1.1", "", `not matched with: "1.1"`},
		{"3.1-0", "", `not matched with: "3.1-0"`},
		{"V1", "", "failed to parse version"},
		{"18446744073709551616", "", "can't parse Major"},
		{"1.18446744073709551616", "", "can't parse Minor"},
		{"0.1.18446744073709551616", "", "can't parse Patch"},
		{"1.2.3-rc18446744073709551616", "", "bad release number format"},
		{"1.2.3-18446744073709551616", "", "can't parse Additional"},
		{"1.2.3-r18446744073709551616", "", "can't parse Revision"},
		{".2.3", "", "minor version requires major to be specified"},
	}

	for i, testData := range data {
		t.Run(fmt.Sprintf("[%d] find %s", i, testData.find), func(t *testing.T) {
			found, err := version.MatchVersion(testData.find, sortedVersions)
			if testData.errMsg != "" {
				require.ErrorContains(t, err, testData.errMsg,
					"Found version: '%s' -> '%s'", testData.find, found)
			} else {
				require.NoError(t, err, "Found version: '%s' -> '%s'", testData.find, found)
			}

			if testData.expected != "" {
				require.Equal(t, testData.expected, found)
			}
		})
	}
}

func TestMatchVersion_NotFoundError(t *testing.T) {
	data := []string{
		"",
		"1234.5678.90",
	}
	for i, verStr := range data {
		t.Run(fmt.Sprintf("[%d] find %s", i, verStr), func(t *testing.T) {
			var errNotFound version.NotFoundError

			_, err := version.MatchVersion(verStr, []version.Version{})
			require.ErrorAs(t, err, &errNotFound)
			require.ErrorIs(t, err, version.ErrNotFound)
			require.ErrorIs(t, fmt.Errorf("resolve version: %w", err), version.ErrNotFound)
			require.Equal(t, fmt.Sprintf("not matched with: %q", verStr), err.Error())
		})
	}

	_, err := version.MatchVersion("V1", []version.Version{})
	require.Error(t, err)
	require.NotErrorIs(t, err, version.ErrNotFound)
}

func TestIsVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"1.2.3", true},
		{"1.2", true},
		{"2.11.0-rc1", true},
		{"2.11-rc1", true},
		{"2-rc1", true},
		{"v1-ga73da88", true},
		{"gc64-3.0.0-1-ga73da88-r123", true},
		{"1,2,3", false},
		{"master", false},
		{"pr/42", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			require.Equal(t, tt.want, version.IsVersion(tt.version))
		})
	}
}

func TestIsNumericVersion(t *testing.T) {
	for _, requested := range []string{"3", "3.8", "3.8.1", "03.08.01"} {
		require.True(t, version.IsNumericVersion(requested), requested)
	}

	for _, requested := range []string{
		"", "v3", "3.", "3..8", "3.8.1.0", "gc64-3.8.1", "3.8.1-rc1", "master", "3\n",
	} {
		require.False(t, version.IsNumericVersion(requested), requested)
	}
}

func TestMatchInstalledVersion(t *testing.T) {
	for _, test := range []struct {
		name      string
		requested string
		installed []string
		want      string
	}{
		{
			name:      "gc64 without revision",
			requested: "3.8.1",
			installed: []string{"gc64-3.8.1"},
			want:      "gc64-3.8.1",
		},
		{
			name:      "patch",
			requested: "3.8.1",
			installed: []string{"gc64-3.8.0-0-r1", "gc64-3.8.1-0-r1", "gc64-3.8.2-0-r1"},
			want:      "gc64-3.8.1-0-r1",
		},
		{
			name:      "numeric fields",
			requested: "03.08.01",
			installed: []string{"gc64-3.8.1-0-r1"},
			want:      "gc64-3.8.1-0-r1",
		},
		{
			name:      "nogc64",
			requested: "3.8.1",
			installed: []string{"nogc64-3.8.1-0-r1"},
			want:      "nogc64-3.8.1-0-r1",
		},
		{
			name:      "debug RC",
			requested: "3.8.1",
			installed: []string{"debug-gc64-3.8.1-rc2-0-r1"},
			want:      "debug-gc64-3.8.1-rc2-0-r1",
		},
		{
			name:      "full older build",
			requested: "nogc64-3.8.1-rc1-0-r1",
			installed: []string{"nogc64-3.8.1-rc1-0-r1", "gc64-3.8.1-0-r2"},
			want:      "nogc64-3.8.1-rc1-0-r1",
		},
		{
			name:      "master",
			requested: "master",
			installed: []string{"master", "gc64-3.8.1-0-r1"},
			want:      "master",
		},
		{
			name:      "hash",
			requested: "a123456",
			installed: []string{"a123456", "gc64-3.8.1-0-r1"},
			want:      "a123456",
		},
		{
			name:      "numeric hash",
			requested: "1234567",
			installed: []string{"1234567", "gc64-3.8.1-0-r1"},
			want:      "1234567",
		},
		{
			name:      "numeric hash exceeds uint64",
			requested: "1234567890123456789012345678901234567890",
			installed: []string{"1234567890123456789012345678901234567890"},
			want:      "1234567890123456789012345678901234567890",
		},
		{
			name:      "exact build without revision",
			requested: "gc64-3.8.1",
			installed: []string{"gc64-3.8.1", "gc64-3.8.1-0-r1"},
			want:      "gc64-3.8.1",
		},
		{
			name:      "duplicate entries and malformed unrelated names",
			requested: "3.8.1",
			installed: []string{"invalid", "3.8", "gc64-3.8.1-0-r1", "gc64-3.8.1-0-r1"},
			want:      "gc64-3.8.1-0-r1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := slices.Clone(test.installed)
			got, err := version.MatchInstalledVersion(test.requested, test.installed)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
			assert.Equal(t, original, test.installed)
		})
	}
}

func TestMatchInstalledVersionAmbiguous(t *testing.T) {
	for _, test := range []struct {
		name      string
		installed []string
	}{
		{"build", []string{"nogc64-3.8.1-0-r1", "gc64-3.8.1-0-r1"}},
		{"debug", []string{"gc64-3.8.1-0-r1", "debug-gc64-3.8.1-0-r1"}},
		{"plain and prefixed", []string{"gc64-3.8.1-0-r1", "3.8.1"}},
		{"revision", []string{"gc64-3.8.1-0-r2", "gc64-3.8.1-0-r1"}},
		{"release", []string{"gc64-3.8.1-rc1-0-r1", "gc64-3.8.1-0-r1"}},
		{"hash", []string{"3.8.1-0-gabcdef2", "3.8.1-0-gabcdef1"}},
		{"additional commits", []string{"3.8.1-2-gabcdef2", "3.8.1-1-gabcdef1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			requested := "3.8.1"
			original := slices.Clone(test.installed)
			got, err := version.MatchInstalledVersion(requested, test.installed)
			require.ErrorIs(t, err, version.ErrAmbiguousInstalledVersion)
			assert.Empty(t, got)
			assert.Contains(t, err.Error(), "more than one installed version matches")
			assert.Contains(t, err.Error(), requested)
			assert.Contains(t, err.Error(), "specify the full version name")
			assert.Contains(t, err.Error(), test.installed[1]+", "+test.installed[0])
			assert.Equal(t, original, test.installed)
		})
	}
}

func TestMatchInstalledVersionNotFound(t *testing.T) {
	installed := []string{"gc64-3.8.1-rc1-0-r1", "gc64-3.8.1-0-r1", "master", ""}

	for _, requested := range []string{
		"", "3", "3.8", "4", "3.9", "3.8.2", "gc64-3", "gc64-3.8.1", "3.8.1-rc1",
		"gc64-3.8.1-0-r2", "missing", "1234567890123456789012345678901234567890",
	} {
		t.Run(requested, func(t *testing.T) {
			got, err := version.MatchInstalledVersion(requested, installed)
			require.ErrorIs(t, err, version.ErrInstalledVersionNotFound)
			assert.Empty(t, got)
			assert.Contains(t, err.Error(), requested)
		})
	}

	got, err := version.MatchInstalledVersion("3", nil)
	require.ErrorIs(t, err, version.ErrInstalledVersionNotFound)
	assert.Empty(t, got)
}
