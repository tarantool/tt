package binary

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/version"
)

func TestParseBinaries(t *testing.T) {
	fileList, err := os.ReadDir("./testdata/bin")
	require.NoError(t, err)
	versions, err := ParseBinaries(fileList, search.ProgramCe, "./testdata/bin")
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
			versions, err := ParseBinaries(fileList, search.ProgramDev, testDir)
			assert.NoError(t, err)
			require.Equal(t, 1, len(versions))
			version := versions[0].Str
			assert.True(t, strings.HasPrefix(version, "tarantool-dev"))
			assert.True(t, strings.HasSuffix(version, "[active]"))
		})
	}
}

func TestParseBinariesNoSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, copy.Copy("./testdata/no_symlink", tmpDir))

	fileList, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	versions, err := ParseBinaries(fileList, search.ProgramCe, tmpDir)
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))
	expectedSortedVersions := []string{"3.1.0-entrypoint-83-gcb0264c3c [active]", "2.10.1"}
	require.Equal(t, len(expectedSortedVersions), len(versions))
	for i := 0; i < len(expectedSortedVersions); i++ {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}
	versions, err = ParseBinaries(fileList, search.ProgramEe, tmpDir)
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))
	expectedSortedVersions = []string{"2.11.1"}
	require.Equal(t, len(expectedSortedVersions), len(versions))
	for i := 0; i < len(expectedSortedVersions); i++ {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}

	// Tarantool exists, but not executable.
	require.NoError(t, os.Chmod(filepath.Join(tmpDir, "tarantool"), 0o440))
	versions, err = ParseBinaries(fileList, search.ProgramEe, tmpDir)
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))
	expectedSortedVersions = []string{"2.11.1"}
	require.Equal(t, len(expectedSortedVersions), len(versions))
	for i := 0; i < len(expectedSortedVersions); i++ {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}

	require.NoError(t, os.Chmod(filepath.Join(tmpDir, "tarantool"), 0o440))
	_, err = ParseBinaries(fileList, search.ProgramCe, tmpDir)
	require.ErrorContains(t, err, "failed to get tarantool version")
}
