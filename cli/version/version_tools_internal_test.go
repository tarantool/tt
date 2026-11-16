package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type returnValueParseVersion struct {
	version Version
	err     error
}

func TestParseVersion(t *testing.T) {
	assert := assert.New(t)

	for input, output := range parseVersionCases() {
		version, err := Parse(input)

		if output.err == nil {
			require.NoError(t, err)
			assert.Equal(output.version, version)
		} else {
			assert.Equal(output.err, err)
		}
	}
}

// TestParseSemverPrerelease checks that Parse reads a prerelease the way
// semver writes it and tt tags are named, 3.0.0-alpha.2, as it reads
// Tarantool's 3.0.0-alpha2, keeping the spelling: a tt binary installation
// of such a tt, tt_3.0.0-alpha.2, is one tt binary list can show and tt
// package install can lay a bundled tt as. Tarantool names no version so,
// and these cases are kept out of the cases the SDK is checked against.
func TestParseSemverPrerelease(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]Version{
		"3.0.0-alpha.2": {
			Major: 3, Release: Release{Type: TypeAlpha, Num: 2, str: "alpha.2"},
			Str: "3.0.0-alpha.2",
		},
		"v3.1.0-rc.10": {
			Major: 3, Minor: 1, Release: Release{Type: TypeRC, Num: 10, str: "rc.10"},
			Str: "v3.1.0-rc.10",
		},
		"3.0.0-beta.1-5-gabc1234": {
			Major: 3, Release: Release{Type: TypeBeta, Num: 1, str: "beta.1"},
			Additional: 5, Hash: "gabc1234", Str: "3.0.0-beta.1-5-gabc1234",
		},
	} {
		got, err := Parse(input)
		require.NoError(t, err, input)
		assert.Equal(t, want, got, input)
		assert.True(t, IsVersion(input), input)
	}

	for _, input := range []string{"3.0.0-alpha.", "3.0.0-alpha..2", "3.0.0-gamma.1"} {
		_, err := Parse(input)
		require.Error(t, err, input)
	}

	older, err := Parse("3.0.0-alpha.2")
	require.NoError(t, err)

	newer, err := Parse("3.0.0-alpha.10")
	require.NoError(t, err)

	release, err := Parse("3.0.0")
	require.NoError(t, err)

	assert.True(t, IsLess(older, newer))
	assert.True(t, IsLess(newer, release))
}

// parseVersionCases returns version strings and what Parse makes of them.
func parseVersionCases() map[string]returnValueParseVersion {
	testCases := make(map[string]returnValueParseVersion)

	testCases["2.10.42-alpha2-91-g08c9b4963-r482"] = returnValueParseVersion{
		Version{
			Major:      2,
			Minor:      10,
			Patch:      42,
			Release:    Release{Type: TypeAlpha, Num: 2, str: "alpha2"},
			Additional: 91,
			Hash:       "g08c9b4963",
			Revision:   482,
			Str:        "2.10.42-alpha2-91-g08c9b4963-r482",
		},
		nil,
	}

	testCases["1.10.13-48-ga3a42eec7-r496"] = returnValueParseVersion{
		Version{
			Major:      1,
			Minor:      10,
			Patch:      13,
			Release:    Release{Type: TypeRelease},
			Additional: 48,
			Hash:       "ga3a42eec7",
			Revision:   496,
			Str:        "1.10.13-48-ga3a42eec7-r496",
		},
		nil,
	}

	testCases["2.11.0-0-gc9673ebb7-r575-nogc64"] = returnValueParseVersion{
		Version{
			Major:      2,
			Minor:      11,
			Patch:      0,
			Release:    Release{Type: TypeRelease},
			Additional: 0,
			Hash:       "gc9673ebb7",
			Revision:   575,
			Str:        "2.11.0-0-gc9673ebb7-r575-nogc64",
		},
		nil,
	}

	testCases["2.11.0-0-gc9673ebb7-r575-gc64"] = returnValueParseVersion{
		Version{
			Major:      2,
			Minor:      11,
			Patch:      0,
			Release:    Release{Type: TypeRelease},
			Additional: 0,
			Hash:       "gc9673ebb7",
			Revision:   575,
			Str:        "2.11.0-0-gc9673ebb7-r575-gc64",
		},
		nil,
	}

	testCases["1.10.123-rc1-100-g2ba6c0"] = returnValueParseVersion{
		Version{
			Major:      1,
			Minor:      10,
			Patch:      123,
			Release:    Release{Type: TypeRC, Num: 1, str: "rc1"},
			Additional: 100,
			Hash:       "g2ba6c0",
			Str:        "1.10.123-rc1-100-g2ba6c0",
		},
		nil,
	}

	testCases["1.2.3-beta22-123"] = returnValueParseVersion{
		Version{
			Major:      1,
			Minor:      2,
			Patch:      3,
			Release:    Release{Type: TypeBeta, Num: 22, str: "beta22"},
			Additional: 123,
			Str:        "1.2.3-beta22-123",
		},
		nil,
	}

	testCases["1.2.3-rc12"] = returnValueParseVersion{
		Version{
			Major:   1,
			Minor:   2,
			Patch:   3,
			Release: Release{Type: TypeRC, Num: 12, str: "rc12"},
			Str:     "1.2.3-rc12",
		},
		nil,
	}

	testCases["3.2.1-entrypoint"] = returnValueParseVersion{
		Version{
			Major:   3,
			Minor:   2,
			Patch:   1,
			Release: Release{Type: TypeNightly, str: "entrypoint"},
			Str:     "3.2.1-entrypoint",
		},
		nil,
	}

	testCases["2.10.0"] = returnValueParseVersion{
		Version{
			Major:   2,
			Minor:   10,
			Patch:   0,
			Release: Release{Type: TypeRelease},
			Str:     "2.10.0",
		},
		nil,
	}

	testCases["v1.2.3"] = returnValueParseVersion{
		Version{
			Major:   1,
			Minor:   2,
			Patch:   3,
			Release: Release{Type: TypeRelease},
			Str:     "v1.2.3",
		},
		nil,
	}

	testCases["nogc64-debug-1.2.3"] = returnValueParseVersion{
		Version{
			Major:     1,
			Minor:     2,
			Patch:     3,
			Release:   Release{Type: TypeRelease},
			Str:       "nogc64-debug-1.2.3",
			BuildName: "nogc64-debug",
		},
		nil,
	}

	testCases["gc64-debug-1.2.3"] = returnValueParseVersion{
		Version{
			Major:     1,
			Minor:     2,
			Patch:     3,
			Release:   Release{Type: TypeRelease},
			Str:       "gc64-debug-1.2.3",
			BuildName: "gc64-debug",
		},
		nil,
	}

	testCases["debug-gc64-1.2.3"] = returnValueParseVersion{
		Version{
			Major:     1,
			Minor:     2,
			Patch:     3,
			Release:   Release{Type: TypeRelease},
			Str:       "debug-gc64-1.2.3",
			BuildName: "debug-gc64",
		},
		nil,
	}

	testCases["debug-test-gc64-test-test-1.2.3"] = returnValueParseVersion{
		Version{
			Major:     1,
			Minor:     2,
			Patch:     3,
			Release:   Release{Type: TypeRelease},
			Str:       "debug-test-gc64-test-test-1.2.3",
			BuildName: "debug-test-gc64-test-test",
		},
		nil,
	}

	testCases["2.8"] = returnValueParseVersion{
		Version{},
		newInvalidVersionError("2.8"),
	}

	testCases["42"] = returnValueParseVersion{
		Version{},
		newInvalidVersionError("42"),
	}

	testCases["2.11.0-0-gc9673ebb7-r575-gc32"] = returnValueParseVersion{
		Version{},
		newInvalidVersionError("2.11.0-0-gc9673ebb7-r575-gc32"),
	}

	return testCases
}

func TestParseTt(t *testing.T) {
	type testCase struct {
		name           string
		inputVer       string
		expectedVer    Version
		isErr          bool
		expectedErrMsg string
	}

	cases := []testCase{
		{
			name:     "basic",
			inputVer: "2.3.1.f7cc1de\n",
			expectedVer: Version{
				Major: 2,
				Minor: 3,
				Patch: 1,
				Hash:  "f7cc1de",
				Str:   "2.3.1.f7cc1de",
			},
			isErr:          false,
			expectedErrMsg: "",
		},
		{
			name:     "pre-release",
			inputVer: "3.0.0-alpha.1.abc1234\n",
			expectedVer: Version{
				Major: 3,
				Minor: 0,
				Patch: 0,
				Hash:  "abc1234",
				Str:   "3.0.0-alpha.1.abc1234",
			},
			isErr:          false,
			expectedErrMsg: "",
		},
		{
			name:     "release candidate",
			inputVer: "3.2.0-rc1.abc1234",
			expectedVer: Version{
				Major: 3,
				Minor: 2,
				Patch: 0,
				Hash:  "abc1234",
				Str:   "3.2.0-rc1.abc1234",
			},
			isErr:          false,
			expectedErrMsg: "",
		},
		{
			name:           "parse error",
			inputVer:       "2.w.1.f7cc1de",
			expectedVer:    Version{},
			isErr:          true,
			expectedErrMsg: `strconv.ParseUint: parsing "w": invalid syntax`,
		},
		{
			name:        "no dots in version",
			inputVer:    "2131f7cc1de",
			expectedVer: Version{},
			isErr:       true,
			expectedErrMsg: `failed to parse version "2131f7cc1de":` +
				` format is not valid`,
		},
		{
			name:        "version does not match",
			inputVer:    "2.1.3.1.f7cc1de",
			expectedVer: Version{},
			isErr:       true,
			expectedErrMsg: `the version of "2.1.3.1" does not match` +
				` <major>.<minor>.<patch> format`,
		},
		{
			name:           "hash does not match",
			inputVer:       "2.1.3.f7cc1de_",
			expectedVer:    Version{},
			isErr:          true,
			expectedErrMsg: `hash "f7cc1de_" has a wrong format`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resVer, err := ParseTt(testCase.inputVer)
			if testCase.isErr {
				require.EqualError(t, err, testCase.expectedErrMsg)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, testCase.expectedVer, resVer)
		})
	}
}
