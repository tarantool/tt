package pack

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTree materializes a name->content map under dir, creating parents.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), dirPerm))
		require.NoError(t, os.WriteFile(path, []byte(content), filePerm))
	}
}

// readArchive decompresses an archive and returns its entries by name.
func readArchive(t *testing.T, path string) map[string]string {
	t.Helper()

	f, err := os.Open(path)
	require.NoError(t, err)

	defer f.Close()

	zr, err := zstd.NewReader(f)
	require.NoError(t, err, "archive must be valid zstd")

	defer zr.Close()

	entries := map[string]string{}
	tr := tar.NewReader(zr)

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err, "archive must be a valid tar stream")

		if header.Typeflag == tar.TypeDir {
			entries[header.Name] = ""

			continue
		}

		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		entries[header.Name] = string(body)
	}

	return entries
}

func TestWriteArchiveRoundTrip(t *testing.T) {
	stage := t.TempDir()
	writeTree(t, stage, map[string]string{
		"VERSION":                             "1.0.0\n",
		"app.manifest.toml":                   "manifest",
		"README.md":                           "readme",
		".rocks/share/tarantool/my-app/i.lua": "return 1",
	})

	dest := filepath.Join(t.TempDir(), "my-app-1.0.0-any.tt")

	sum, err := writeArchive(stage, dest)
	require.NoError(t, err)
	assert.Len(t, sum, 64, "sha256 hex is 64 chars")

	entries := readArchive(t, dest)
	assert.Equal(t, "1.0.0\n", entries["VERSION"])
	assert.Equal(t, "manifest", entries["app.manifest.toml"])
	assert.Equal(t, "readme", entries["README.md"])
	assert.Equal(t, "return 1", entries[".rocks/share/tarantool/my-app/i.lua"])

	// Paths are slash-separated and carry no leading "./" or staging prefix.
	for name := range entries {
		assert.NotContains(t, name, "\\")
		assert.False(t, filepath.IsAbs(name), "%q must be relative", name)
		assert.NotContains(t, name, "..")
	}
}

// TestWriteArchiveReproducible is the reason this package does not reuse
// cli/pack.WriteTarArchive: tar.FileInfoHeader stamps real mtimes, so the same
// content packed twice would differ. Identical content must yield an identical
// archive byte for byte.
func TestWriteArchiveReproducible(t *testing.T) {
	files := map[string]string{
		"VERSION":           "1.0.0\n",
		"app.manifest.toml": "manifest",
		"a/b/c.lua":         "return 1",
	}

	first := t.TempDir()
	writeTree(t, first, files)

	destA := filepath.Join(t.TempDir(), "a.tt")
	sumA, err := writeArchive(first, destA)
	require.NoError(t, err)

	// A second tree with the same content but written later, so every mtime
	// differs from the first tree's.
	time.Sleep(10 * time.Millisecond)

	second := t.TempDir()
	writeTree(t, second, files)

	now := time.Now().Add(2 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(second, "VERSION"), now, now))

	destB := filepath.Join(t.TempDir(), "b.tt")
	sumB, err := writeArchive(second, destB)
	require.NoError(t, err)

	assert.Equal(t, sumA, sumB, "same content must yield the same checksum")

	bytesA, err := os.ReadFile(destA)
	require.NoError(t, err)
	bytesB, err := os.ReadFile(destB)
	require.NoError(t, err)
	assert.Equal(t, bytesA, bytesB, "archives must be byte-identical")
}

// TestWriteArchivePreservesExecBit covers the one permission bit that survives
// normalization: _runtime/ binaries are useless without it.
func TestWriteArchivePreservesExecBit(t *testing.T) {
	stage := t.TempDir()
	writeTree(t, stage, map[string]string{"plain.lua": "x"})

	binPath := filepath.Join(stage, "_runtime", "tt", "bin", "tt")
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), dirPerm))
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755))

	dest := filepath.Join(t.TempDir(), "x.tt")
	_, err := writeArchive(stage, dest)
	require.NoError(t, err)

	modes := map[string]int64{}

	f, err := os.Open(dest)
	require.NoError(t, err)

	defer f.Close()

	zr, err := zstd.NewReader(f)
	require.NoError(t, err)

	defer zr.Close()

	tr := tar.NewReader(zr)

	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		require.NoError(t, err)
		modes[header.Name] = header.Mode

		// Normalized ownership keeps the archive independent of who packed it.
		assert.Zero(t, header.Uid)
		assert.Zero(t, header.Gid)
		assert.True(t, header.ModTime.Equal(archiveModTime),
			"%q must carry the normalized mtime", header.Name)
	}

	assert.Equal(t, int64(archiveExecMode), modes["_runtime/tt/bin/tt"])
	assert.Equal(t, int64(archiveFileMode), modes["plain.lua"])
}

// TestWriteArchiveNoPartialOnFailure guards the cleanup path: a failed write
// must not leave a truncated archive that only fails later, at install time.
func TestWriteArchiveNoPartialOnFailure(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.tt")

	_, err := writeArchive(filepath.Join(t.TempDir(), "does-not-exist"), dest)
	require.Error(t, err)

	_, statErr := os.Stat(dest)
	assert.True(t, os.IsNotExist(statErr), "no archive must be left behind")
}
