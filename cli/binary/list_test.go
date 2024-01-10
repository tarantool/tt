package binary

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/version"
)

func TestParseBinaries(t *testing.T) {
	fileList, err := os.ReadDir("./testdata/bin")
	require.NoError(t, err)
	versions, err := ParseBinaries(fileList, "tarantool", "./testdata/bin")
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))
	expectedSortedVersions := []string{"master", "2.10.5", "2.8.6 [active]", "1.10.0", "0000000"}
	require.Equal(t, len(expectedSortedVersions), len(versions))
	for i := 0; i < len(expectedSortedVersions); i++ {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}
}

func TestParseBinariesTarantoolDev(t *testing.T) {
	for _, dir := range []string{"bin", "bin_symlink_broken"} {
		t.Run(dir, func(t *testing.T) {
			testDir := fmt.Sprintf("./testdata/tarantool_dev/%s", dir)
			fileList, err := os.ReadDir(testDir)
			assert.NoError(t, err)
			versions, err := ParseBinaries(fileList, "tarantool-dev", testDir)
			assert.NoError(t, err)
			assert.Equal(t, 1, len(versions))
			version := versions[0].Str
			assert.True(t, strings.HasPrefix(version, "tarantool-dev"))
			assert.True(t, strings.HasSuffix(version, "[active]"))
		})
	}
}
