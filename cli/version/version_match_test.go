package version_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/version"
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

	for i, v := range data {
		t.Run(fmt.Sprintf("[%d] find %s", i, v.find), func(t *testing.T) {
			found, err := version.MatchVersion(v.find, sortedVersions)
			if v.errMsg != "" {
				require.ErrorContains(t, err, v.errMsg,
					"Found version: '%s' -> '%s'", v.find, found)
			} else {
				require.NoError(t, err, "Found version: '%s' -> '%s'", v.find, found)
			}
			if v.expected != "" {
				require.Equal(t, v.expected, found)
			}
		})
	}
}

func TestMatchVersion_NotFoundError(t *testing.T) {
	data := []string{
		"",
		"1234.5678.90",
	}
	for i, v := range data {
		t.Run(fmt.Sprintf("[%d] find %s", i, v), func(t *testing.T) {
			var errNotFound version.NotFoundError
			_, err := version.MatchVersion(v, []version.Version{})
			require.True(t, errors.As(err, &errNotFound))
			require.Equal(t, fmt.Sprintf("not matched with: %q", v), err.Error())
		})
	}
}

func TestIsVersion(t *testing.T) {
	tests := []struct {
		version string
		strict  bool
		want    bool
	}{
		{"1.2.3", true, true},
		{"1.2", false, true},
		{"2.11.0-rc1", true, true},
		{"2.11-rc1", true, false},
		{"2-rc1", false, true},
		{"v1-ga73da88", false, true},
		{"gc64-3.0.0-1-ga73da88-r123", false, true},
		{"gc64-3.0.0-1-ga73da88-r123", true, true},
		{"1,2,3", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := version.IsVersion(tt.version, tt.strict); got != tt.want {
				t.Errorf("IsVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
