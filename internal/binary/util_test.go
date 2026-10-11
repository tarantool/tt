package binary_test

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestWriteYaml(t *testing.T) {
	type book struct {
		Title  string
		Author string
		Pages  int
	}

	type library struct {
		Books []*book
	}

	lib := library{Books: []*book{
		{"title1", "author1", 100},
		{"title2", "author2", 200},
	}}

	tempDir := t.TempDir()
	require.NoError(t, binary.WriteYaml(filepath.Join(tempDir, "library"), &lib))

	file, err := os.Open(filepath.Join(tempDir, "library"))
	require.NoError(t, err)

	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)
	scanner.Scan()
	require.Contains(t, scanner.Text(), "books:")
	scanner.Scan()
	require.Equal(t, "- title: title1", scanner.Text())
	scanner.Scan()
	require.Equal(t, "  author: author1", scanner.Text())
	scanner.Scan()
	require.Equal(t, "  pages: 100", scanner.Text())
	scanner.Scan()
	require.Equal(t, "- title: title2", scanner.Text())
	scanner.Scan()
	require.Equal(t, "  author: author2", scanner.Text())
	scanner.Scan()
	require.Equal(t, "  pages: 200", scanner.Text())
}

func TestCreateSymlink(t *testing.T) {
	tempDir := t.TempDir()
	targetFile, err := os.Create(filepath.Join(tempDir, "tgtFile.txt"))
	require.NoError(t, err)

	_ = targetFile.Close()

	// No overwrite.
	require.NoError(t, binary.CreateSymlink(targetFile.Name(), filepath.Join(tempDir, "first_link"),
		false))
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))

	targetPath, err := os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, targetFile.Name(), targetPath)

	// Overwrite flag is set, but symlink does not exist.
	require.NoError(t, binary.CreateSymlink(
		targetFile.Name(), filepath.Join(tempDir, "second_link"), true))
	assert.FileExists(t, filepath.Join(tempDir, "second_link"))

	targetPath, err = os.Readlink(filepath.Join(tempDir, "second_link"))
	require.NoError(t, err)
	assert.Equal(t, targetFile.Name(), targetPath)

	// Overwrite existing symlink.
	require.NoError(t, binary.CreateSymlink("./tgtFile.txt", filepath.Join(tempDir, "first_link"),
		true))
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))

	targetPath, err = os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, "./tgtFile.txt", targetPath)

	// Don't overwrite existing.
	require.Error(t, binary.CreateSymlink("./some_file", filepath.Join(tempDir, "first_link"),
		false))
	// Check existing link is not updated.
	assert.FileExists(t, filepath.Join(tempDir, "first_link"))

	targetPath, err = os.Readlink(filepath.Join(tempDir, "first_link"))
	require.NoError(t, err)
	assert.Equal(t, "./tgtFile.txt", targetPath)
}
