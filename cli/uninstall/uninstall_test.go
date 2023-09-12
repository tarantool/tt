package uninstall

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/version"
)

const testDirName = "uninstall-test-dir"

func TestGetList(t *testing.T) {
	assert := assert.New(t)
	workDir := t.TempDir()

	binDir := filepath.Join(workDir, "bin")
	err := os.Mkdir(binDir, os.ModePerm)
	require.NoError(t, err)

	cfgData := []byte("tt:\n  app:\n    bin_dir: " + binDir)
	cfgPath := filepath.Join(workDir, "tt.yaml")

	err = os.WriteFile(cfgPath, cfgData, 0400)
	require.NoError(t, err)

	files := []string{
		"tt" + version.FsSeparator + "1.2.3",
		"tarantool" + version.FsSeparator + "1.2.10",
		"tarantool-ee" + version.FsSeparator + "master",
	}
	for _, file := range files {
		f, err := os.Create(filepath.Join(binDir, file))
		require.NoError(t, err)
		f.Close()
	}

	cliOpts, _, err := configure.GetCliOpts(cfgPath)
	require.NoError(t, err)
	result := GetList(cliOpts, "tt")
	assert.Equal(result, []string{"1.2.3"})

	result = GetList(cliOpts, "tarantool")
	assert.Equal(result, []string{"1.2.10"})

	result = GetList(cliOpts, "tarantool-ee")
	assert.Equal(result, []string{"master"})
}

func TestSearchLatestVersion(t *testing.T) {
	type testCase struct {
		name        string
		linkName    string
		binDst      string
		headerDst   string
		expectedVer string
		isErr       bool
	}

	cases := []testCase{
		{
			name:        "basic",
			linkName:    "tarantool",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_basic",
			expectedVer: "tarantool_3.0.0-entrypoint",
		},
		{
			name:        "no includes",
			linkName:    "tarantool",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_invalid",
			expectedVer: "tarantool-ee_2.8.4-0-r510",
		},
		{
			name:        "tarantool-dev",
			linkName:    "tarantool",
			binDst:      "./testdata/bin_dev",
			headerDst:   "./testdata/inc_basic",
			expectedVer: "",
		},
		{
			name:        "hash version",
			linkName:    "tarantool",
			binDst:      "./testdata/bin_hash",
			headerDst:   "./testdata/inc_hash",
			expectedVer: "tarantool_aaaaaaa",
		},
		{
			name:        "hash invalid headers",
			linkName:    "tarantool",
			binDst:      "./testdata/bin_hash",
			headerDst:   "./testdata/inc_invalid_hash",
			expectedVer: "tarantool_bbbbbbb",
		},
		{
			name:        "tt, include-dir basic",
			linkName:    "tt",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_basic",
			expectedVer: "tt_2.0.0",
		},
		// Test that include dir doesn't affect the search for `tt`.
		{
			name:        "tt, include-dir invalid",
			linkName:    "tt",
			binDst:      "./testdata/bin_basic",
			headerDst:   "./testdata/inc_invalid",
			expectedVer: "tt_2.0.0",
		},
		{
			name:      "filename as a bin dir",
			linkName:  "tt",
			binDst:    "./testdata/bin_basic/tarantool",
			headerDst: "./testdata/inc_basic",
			isErr:     true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ver, err := searchLatestVersion(tc.linkName, tc.binDst, tc.headerDst)
			if !tc.isErr {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedVer, ver)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
