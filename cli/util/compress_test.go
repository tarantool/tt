package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTgz(t *testing.T) {
	tempDir := t.TempDir()
	require.NoError(t, ExtractTarGz(filepath.Join("testdata", "arch.tgz"), tempDir))
	/* Archive file tree:
	.
	├── file_link -> file.sh
	├── file.sh
	└── subdir
	    └── file.txt
	*/
	stat, err := os.Stat(filepath.Join(tempDir, "test_archive", "file.sh"))
	require.NoError(t, err)
	assert.True(t, stat.Mode().Perm()&0100 != 0) // Executable bit is set.
	linkTarget, err := os.Readlink(filepath.Join(tempDir, "test_archive", "file_link"))
	require.NoError(t, err)
	assert.Equal(t, "file.sh", linkTarget)
	assert.FileExists(t, filepath.Join(tempDir, "test_archive", "file_link"))
	assert.FileExists(t, filepath.Join(tempDir, "test_archive", "subdir", "file.txt"))
}

func TestExtractTgzErrors(t *testing.T) {
	tempDir := t.TempDir()
	require.Error(t, ExtractTarGz("non_existing_file", tempDir))
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "text_file.tgz"), []byte("text"),
		os.FileMode(0664)))
	require.Error(t, ExtractTarGz(filepath.Join(tempDir, "text_file.tgz"), tempDir))
}
