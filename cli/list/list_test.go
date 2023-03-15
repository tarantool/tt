package list

import (
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/version"
)

func TestParseBinaries(t *testing.T) {
	fileList, err := os.ReadDir("./testdata/bin")
	require.NoError(t, err)
	versions, err := parseBinaries(fileList, "tarantool", "./testdata/bin")
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))
	expectedSortedVersions := []string{"master", "2.10.5", "2.8.6 [active]", "1.10.0"}
	require.Equal(t, len(expectedSortedVersions), len(versions))
	for i := 0; i < len(expectedSortedVersions); i++ {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}
}
