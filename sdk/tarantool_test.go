package sdk_test

import (
	"cmp"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
)

// TestParseTarantoolVersion parses the version strings tt's own version
// parser is tested with.
func TestParseTarantoolVersion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want sdk.TarantoolVersion
	}{
		{"2.10.42-alpha2-91-g08c9b4963-r482", sdk.TarantoolVersion{
			Major: 2, Minor: 10, Patch: 42, Release: sdk.ReleaseAlpha, ReleaseNum: 2,
			Commits: 91, Hash: "08c9b4963", Revision: 482,
		}},
		{"1.10.13-48-ga3a42eec7-r496", sdk.TarantoolVersion{
			Major: 1, Minor: 10, Patch: 13, Commits: 48, Hash: "a3a42eec7", Revision: 496,
		}},
		{"2.11.0-0-gc9673ebb7-r575-nogc64", sdk.TarantoolVersion{
			Major: 2, Minor: 11, Hash: "c9673ebb7", Revision: 575,
		}},
		{"2.11.0-0-gc9673ebb7-r575-gc64", sdk.TarantoolVersion{
			Major: 2, Minor: 11, Hash: "c9673ebb7", Revision: 575,
		}},
		{"1.10.123-rc1-100-g2ba6c0", sdk.TarantoolVersion{
			Major: 1, Minor: 10, Patch: 123, Release: sdk.ReleaseRC, ReleaseNum: 1,
			Commits: 100, Hash: "2ba6c0",
		}},
		{"1.2.3-beta22-123", sdk.TarantoolVersion{
			Major: 1, Minor: 2, Patch: 3, Release: sdk.ReleaseBeta, ReleaseNum: 22, Commits: 123,
		}},
		{"1.2.3-rc12", sdk.TarantoolVersion{
			Major: 1, Minor: 2, Patch: 3, Release: sdk.ReleaseRC, ReleaseNum: 12,
		}},
		{"3.2.1-entrypoint", sdk.TarantoolVersion{
			Major: 3, Minor: 2, Patch: 1, Release: sdk.ReleaseNightly,
		}},
		{"2.10.0", sdk.TarantoolVersion{Major: 2, Minor: 10}},
		{"v1.2.3", sdk.TarantoolVersion{Major: 1, Minor: 2, Patch: 3}},
		{"nogc64-debug-1.2.3", sdk.TarantoolVersion{
			Major: 1, Minor: 2, Patch: 3, BuildName: "nogc64-debug",
		}},
		{"debug-test-gc64-test-test-1.2.3", sdk.TarantoolVersion{
			Major: 1, Minor: 2, Patch: 3, BuildName: "debug-test-gc64-test-test",
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.in, func(t *testing.T) {
			t.Parallel()

			got, err := sdk.ParseTarantoolVersion(testCase.in)
			require.NoError(t, err)

			want := testCase.want

			want.Raw = testCase.in
			assert.Equal(t, want, got)
			assert.Equal(t, testCase.in, got.String())
			assert.Equal(t, sdk.EditionUnknown, got.Edition)
		})
	}
}

func TestParseTarantoolVersionInvalid(t *testing.T) {
	t.Parallel()

	for _, invalid := range []string{
		"2.8", "42", "", "2.11.0-0-gc9673ebb7-r575-gc32", "3.0.0-gamma1",
		"99999999999999999999.0.0",
	} {
		t.Run(invalid, func(t *testing.T) {
			t.Parallel()

			_, err := sdk.ParseTarantoolVersion(invalid)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid Tarantool version")
		})
	}
}

// TestTarantoolVersionString spells a version built from its fields.
func TestTarantoolVersionString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   sdk.TarantoolVersion
		want string
	}{
		{sdk.TarantoolVersion{Major: 3, Minor: 2, Patch: 1}, "3.2.1"},
		{sdk.TarantoolVersion{Major: 3, Release: sdk.ReleaseRC, ReleaseNum: 2}, "3.0.0-rc2"},
		{sdk.TarantoolVersion{Major: 3, Minor: 3, Release: sdk.ReleaseNightly}, "3.3.0-entrypoint"},
		{
			sdk.TarantoolVersion{Major: 2, Minor: 11, Patch: 1, Hash: "96877bd", Revision: 579},
			"2.11.1-0-g96877bd-r579",
		},
		{sdk.TarantoolVersion{Major: 1, Minor: 2, Patch: 3, Commits: 5}, "1.2.3-5"},
		{sdk.TarantoolVersion{Major: 1, Minor: 2, Patch: 3, BuildName: "debug"}, "debug-1.2.3"},
		{sdk.TarantoolVersion{Major: 1, Raw: "as-reported"}, "as-reported"},
	}

	for _, testCase := range cases {
		t.Run(testCase.want, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, testCase.in.String())

			if testCase.in.Raw != "" {
				return
			}

			// A version spelled from its fields parses back to the same fields.
			parsed, err := sdk.ParseTarantoolVersion(testCase.in.String())
			require.NoError(t, err)

			parsed.Raw = ""
			assert.Equal(t, testCase.in, parsed)
		})
	}
}

// TestTarantoolVersionCompare sorts versions shuffled out of the order they
// are listed in, oldest first, and checks every pair.
func TestTarantoolVersionCompare(t *testing.T) {
	t.Parallel()

	ordered := []string{
		"1.10.13-48-ga3a42eec7-r496",
		"1.10.123-rc1-100-g2ba6c0",
		"2.10.0",
		"2.10.42-alpha2-91-g08c9b4963-r482",
		"2.11.0-0-gc9673ebb7-r575",
		"2.11.0-0-gc9673ebb7-r576",
		"2.11.0-3-gc9673ebb7",
		"3.0.0-entrypoint",
		"3.0.0-alpha1",
		"3.0.0-alpha2",
		"3.0.0-beta1",
		"3.0.0-rc1",
		"3.0.0-rc2",
		"3.0.0",
		"3.0.1",
		"3.1.0",
	}

	versions := make([]sdk.TarantoolVersion, 0, len(ordered))

	for _, s := range ordered {
		v, err := sdk.ParseTarantoolVersion(s)
		require.NoError(t, err)

		versions = append(versions, v)
	}

	for i, left := range versions {
		for j, right := range versions {
			assert.Equal(t, cmp.Compare(i, j), left.Compare(right), "%s vs %s", left, right)
		}
	}

	shuffled := slices.Clone(versions)
	slices.Reverse(shuffled)
	slices.SortFunc(shuffled, sdk.TarantoolVersion.Compare)
	assert.Equal(t, versions, shuffled)
}

func TestTarantoolVersionCompareIgnoresIdentity(t *testing.T) {
	t.Parallel()

	ce := sdk.TarantoolVersion{Major: 3, Minor: 2, Hash: "aaaa", Edition: sdk.EditionCE}
	ee := sdk.TarantoolVersion{
		Major: 3, Minor: 2, Hash: "bbbb", Edition: sdk.EditionEE, BuildName: "debug", Raw: "x",
	}

	assert.Equal(t, 0, ce.Compare(ee))
}

func TestTarantoolVersionAtLeast(t *testing.T) {
	t.Parallel()

	version := sdk.TarantoolVersion{Major: 3, Minor: 2, Patch: 1}

	assert.True(t, version.AtLeast(3, 2, 1))
	assert.True(t, version.AtLeast(3, 2, 0))
	assert.True(t, version.AtLeast(2, 11, 9))
	assert.False(t, version.AtLeast(3, 2, 2))
	assert.False(t, version.AtLeast(4, 0, 0))

	rc := sdk.TarantoolVersion{Major: 3, Release: sdk.ReleaseRC, ReleaseNum: 1}
	assert.False(t, rc.AtLeast(3, 0, 0), "a release candidate precedes its release")
	assert.True(t, rc.AtLeast(2, 11, 0))
}

func TestReleaseTypeString(t *testing.T) {
	t.Parallel()

	assert.Empty(t, sdk.ReleaseGA.String())
	assert.Equal(t, "entrypoint", sdk.ReleaseNightly.String())
	assert.Equal(t, "alpha", sdk.ReleaseAlpha.String())
	assert.Equal(t, "beta", sdk.ReleaseBeta.String())
	assert.Equal(t, "rc", sdk.ReleaseRC.String())
}
