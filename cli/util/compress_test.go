package util_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/util"
)

func TestExtractTgz(t *testing.T) {
	tempDir := t.TempDir()
	require.NoError(t, util.ExtractTarGz(filepath.Join("testdata", "arch.tgz"), tempDir))

	/* Archive file tree:
	.
	├── file_link -> file.sh
	├── file.sh
	└── subdir
	    └── file.txt
	*/
	stat, err := os.Stat(filepath.Join(tempDir, "test_archive", "file.sh"))
	require.NoError(t, err)
	assert.NotZero(t, stat.Mode().Perm()&0o100) // Executable bit is set.

	linkTarget, err := os.Readlink(filepath.Join(tempDir, "test_archive", "file_link"))
	require.NoError(t, err)
	assert.Equal(t, "file.sh", linkTarget)
	assert.FileExists(t, filepath.Join(tempDir, "test_archive", "file_link"))
	assert.FileExists(t, filepath.Join(tempDir, "test_archive", "subdir", "file.txt"))
}

func TestExtractTgzErrors(t *testing.T) {
	tempDir := t.TempDir()
	require.Error(t, util.ExtractTarGz("non_existing_file", tempDir))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "text_file.tgz"), []byte("text"),
		os.FileMode(0o664)))
	require.Error(t, util.ExtractTarGz(filepath.Join(tempDir, "text_file.tgz"), tempDir))
}
