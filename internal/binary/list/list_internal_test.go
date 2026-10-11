package list

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/otiai10/copy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestParseBinaries(t *testing.T) {
	fileList, err := os.ReadDir("../testdata/bin")
	require.NoError(t, err)

	versions, err := ParseBinaries(fileList, binary.ProgramCe, "../testdata/bin")
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))

	expectedSortedVersions := []string{"master", "2.10.5", "2.8.6 [active]", "1.10.0", "0000000"}
	require.Len(t, versions, len(expectedSortedVersions))

	for i := range expectedSortedVersions {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}
}

func TestParseBinariesWithLocalBuild(t *testing.T) {
	for _, dir := range []string{"bin", "bin_symlink_broken"} {
		t.Run(dir, func(t *testing.T) {
			testDir := "../testdata/tarantool_dev/" + dir
			fileList, err := os.ReadDir(testDir)
			require.NoError(t, err)

			versions, err := ParseBinaries(fileList, binary.ProgramCe, testDir)
			require.NoError(t, err)

			// A local build is displayed separately, never offered as a version to switch to.
			if dir == "bin" {
				require.Len(t, versions, 2)
				assert.Equal(t, "1.10.0", versions[0].Str)
				assert.Equal(t, "2.10.7", versions[1].Str)
			} else {
				assert.Empty(t, versions)
			}
		})
	}
}

func TestParseBinariesNoSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, copy.Copy("../testdata/no_symlink", tmpDir))

	fileList, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	versions, err := ParseBinaries(fileList, binary.ProgramCe, tmpDir)
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))

	expectedSortedVersions := []string{"3.1.0-entrypoint-83-gcb0264c3c [active]", "2.10.1"}
	require.Len(t, versions, len(expectedSortedVersions))

	for i := range expectedSortedVersions {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}

	versions, err = ParseBinaries(fileList, binary.ProgramEe, tmpDir)
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))

	expectedSortedVersions = []string{"2.11.1"}
	require.Len(t, versions, len(expectedSortedVersions))

	for i := range expectedSortedVersions {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}

	// Tarantool exists, but not executable.
	require.NoError(t, os.Chmod(filepath.Join(tmpDir, "tarantool"), 0o440))

	versions, err = ParseBinaries(fileList, binary.ProgramEe, tmpDir)
	require.NoError(t, err)
	sort.Stable(sort.Reverse(version.VersionSlice(versions)))

	expectedSortedVersions = []string{"2.11.1"}
	require.Len(t, versions, len(expectedSortedVersions))

	for i := range expectedSortedVersions {
		assert.Equal(t, expectedSortedVersions[i], versions[i].Str)
	}

	require.NoError(t, os.Chmod(filepath.Join(tmpDir, "tarantool"), 0o440))

	_, err = ParseBinaries(fileList, binary.ProgramCe, tmpDir)
	require.ErrorContains(t, err, "failed to get tarantool version")
}
