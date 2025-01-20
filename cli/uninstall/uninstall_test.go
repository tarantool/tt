package uninstall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/version"
)

func TestGetAvailableVersions(t *testing.T) {
	assert := assert.New(t)
	workDir := t.TempDir()

	type testCase struct {
		name     string
		program  string
		dir      string
		expected []string
		isErr    bool
	}
	base_cases := []testCase{
		{
			program: search.ProgramTt,
			expected: []string{
				"1.1.0",
				"1.2.3",
				"1.3.4",
				"2.1.0",
			},
		},
		{
			program: search.ProgramCe,
			expected: []string{
				"1.10.0",
				"2.10.8",
				"2.11.8",
				"3.1.2",
				"master",
				"aaaaaaa",
				"cececece",
			},
		},
		{
			program: search.ProgramEe,
			expected: []string{
				"1.10.15-0-r543",
				"2.8.4-0-r510",
				"gc64-2.11.5-0-r662",
				"gc64-3.3.0-0-r43",
				"master",
				"bbbbbbb",
				"eeeeeee",
			},
		},
	}

	// Setup test cases
	cases := []testCase{}
	for _, set_name := range []string{"empty", "single", "multiple"} {
		dir := filepath.Join(workDir, "dir_"+set_name)
		err := os.Mkdir(dir, os.ModePerm)
		require.NoError(t, err)
		for _, base_case := range base_cases {
			tc := base_case
			tc.name = set_name + "_" + base_case.program
			tc.dir = dir
			switch set_name {
			case "empty":
				tc.expected = []string{}
			case "single":
				tc.expected = base_case.expected[0:1]
			case "multiple":
				tc.expected = base_case.expected
			}
			for _, ver := range tc.expected {
				fileName := tc.program + version.FsSeparator + ver
				f, err := os.Create(filepath.Join(dir, fileName))
				require.NoError(t, err)
				f.Close()
			}
			cases = append(cases, tc)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			versions, err := GetAvailableVersions(tc.program, tc.dir)
			if !tc.isErr {
				assert.NoError(err)
				assert.Equal(tc.expected, versions)
			} else {
				assert.Error(err)
			}
		})
	}
}

func TestSearchLatestVersion(t *testing.T) {
	type testCase struct {
		name        string
		program     string
		binDst      string
		headerDst   string
		expectedVer string
		isErr       bool
	}

	cases := []testCase{
		{
			name:        "basic",
			program:     "tarantool",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_basic",
			expectedVer: "tarantool_3.0.0-entrypoint",
		},
		{
			name:        "no includes",
			program:     "tarantool-ee",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_invalid",
			expectedVer: "tarantool-ee_2.8.4-0-r510",
		},
		{
			name:        "tarantool-dev",
			program:     "tarantool-dev",
			binDst:      "./testdata/bin_dev",
			headerDst:   "./testdata/inc_basic",
			expectedVer: "",
		},
		{
			name:        "hash version",
			program:     "tarantool",
			binDst:      "./testdata/bin_hash",
			headerDst:   "./testdata/inc_hash",
			expectedVer: "tarantool_aaaaaaa",
		},
		{
			name:        "hash invalid headers",
			program:     "tarantool",
			binDst:      "./testdata/bin_hash",
			headerDst:   "./testdata/inc_invalid_hash",
			expectedVer: "tarantool_bbbbbbb",
		},
		{
			name:        "tt, include-dir basic",
			program:     "tt",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_basic",
			expectedVer: "tt_2.0.0",
		},
		// Test that include dir doesn't affect the search for `tt`.
		{
			name:        "tt, include-dir invalid",
			program:     "tt",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_invalid",
			expectedVer: "tt_2.0.0",
		},
		{
			name:      "filename as a bin dir",
			program:   "tt",
			binDst:    "./testdata/bin_basic/tarantool",
			headerDst: "./testdata/inc_basic",
			isErr:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ver, err := searchLatestVersion(tc.program, tc.binDst, tc.headerDst)
			if !tc.isErr {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedVer, ver)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestGetAllVersionFormats(t *testing.T) {
	type testCase struct {
		name             string
		programName      string
		ttVersion        string
		expectedVersions []string
		expectedError    error
	}

	cases := []testCase{
		{
			name:             "without prefix",
			programName:      search.ProgramTt,
			ttVersion:        "1.2.3",
			expectedVersions: []string{"1.2.3", "v1.2.3"},
			expectedError:    nil,
		},
		{
			name:             "with prefix",
			programName:      search.ProgramTt,
			ttVersion:        "v1.2.3",
			expectedVersions: []string{"v1.2.3"},
			expectedError:    nil,
		},
		{
			name:             "not tt program",
			programName:      search.ProgramCe,
			ttVersion:        "1.2.3",
			expectedVersions: []string{"1.2.3"},
			expectedError:    nil,
		},
		{
			name:             "not <major.minor.patch> format",
			programName:      search.ProgramCe,
			ttVersion:        "e902206",
			expectedVersions: []string{"e902206"},
			expectedError:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ttVersions, err := getAllTtVersionFormats(tc.programName, tc.ttVersion)
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedVersions, ttVersions)
		})
	}
}
