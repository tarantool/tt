package untar_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/untar"
)

// entry is an entry of a test archive.
type entry struct {
	name     string
	kind     byte
	mode     int64
	content  string
	linkname string
}

func file(name, content string, mode int64) entry {
	return entry{name: name, kind: tar.TypeReg, mode: mode, content: content, linkname: ""}
}

func dir(name string) entry {
	return entry{name: name, kind: tar.TypeDir, mode: 0o755, content: "", linkname: ""}
}

func symlink(name, target string) entry {
	return entry{name: name, kind: tar.TypeSymlink, mode: 0o777, content: "", linkname: target}
}

// writeArchive writes a tar.gz archive of entries into dir.
func writeArchive(t *testing.T, dir string, entries ...entry) string {
	t.Helper()

	path := filepath.Join(dir, "archive.tar.gz")

	archive, err := os.Create(path)
	require.NoError(t, err)

	compressed := gzip.NewWriter(archive)
	writer := tar.NewWriter(compressed)

	for _, item := range entries {
		header := &tar.Header{
			Typeflag: item.kind, Name: item.name, Mode: item.mode, Linkname: item.linkname,
			Size: int64(len(item.content)),
		}
		require.NoError(t, writer.WriteHeader(header))

		_, err := writer.Write([]byte(item.content))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, compressed.Close())
	require.NoError(t, archive.Close())

	return path
}

// layout makes a directory with the archive, the destination in it and a
// directory beside the destination the extraction must not write to.
func layout(t *testing.T) (string, string, string) {
	t.Helper()

	base := t.TempDir()
	dst := filepath.Join(base, "dst")
	outside := filepath.Join(base, "outside")

	require.NoError(t, os.Mkdir(dst, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))

	return base, dst, outside
}

// createdMode returns the permissions a file created with mode gets, the
// umask applied.
func createdMode(t *testing.T, mode os.FileMode) os.FileMode {
	t.Helper()

	path := filepath.Join(t.TempDir(), "reference")

	reference, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, mode)
	require.NoError(t, err)
	require.NoError(t, reference.Close())

	info, err := os.Stat(path)
	require.NoError(t, err)

	return info.Mode().Perm()
}

func TestExtractTarGz_WellFormed(t *testing.T) {
	base, dst, _ := layout(t)

	archive := writeArchive(t, base,
		dir("./"),
		file("./app/bin/run.sh", "#!/bin/sh\n", 0o755),
		dir("./app/"),
		file("app/data/conf.yml", "a: 1\n", 0o640),
		file("top.txt", "top", 0o644),
		symlink("app/bin/run", "run.sh"),
		symlink("app/link-to-data", "data/../data/conf.yml"),
		symlink("app/dangling", "missing"),
	)

	require.NoError(t, untar.ExtractTarGz(archive, dst,
		untar.Options{Symlinks: true, EntryModes: true}))

	content, err := os.ReadFile(filepath.Join(dst, "app", "bin", "run"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n", string(content))

	info, err := os.Stat(filepath.Join(dst, "app", "bin", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, createdMode(t, 0o755), info.Mode().Perm())

	info, err = os.Stat(filepath.Join(dst, "app", "data", "conf.yml"))
	require.NoError(t, err)
	assert.Equal(t, createdMode(t, 0o640), info.Mode().Perm())

	info, err = os.Stat(filepath.Join(dst, "app", "data"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, createdMode(t, 0o755), info.Mode().Perm())

	target, err := os.Readlink(filepath.Join(dst, "app", "link-to-data"))
	require.NoError(t, err)
	assert.Equal(t, "data/../data/conf.yml", target)

	target, err = os.Readlink(filepath.Join(dst, "app", "dangling"))
	require.NoError(t, err)
	assert.Equal(t, "missing", target)

	assert.FileExists(t, filepath.Join(dst, "top.txt"))
}

func TestExtractTarGz_DefaultModes(t *testing.T) {
	base, dst, _ := layout(t)

	archive := writeArchive(t, base, file("tarantool", "binary", 0o755))
	require.NoError(t, untar.ExtractTarGz(archive, dst, untar.Options{}))

	info, err := os.Stat(filepath.Join(dst, "tarantool"))
	require.NoError(t, err)
	assert.Equal(t, createdMode(t, 0o666), info.Mode().Perm())
}

func TestExtractTarGz_CreatesDestination(t *testing.T) {
	base, dst, _ := layout(t)

	archive := writeArchive(t, base, file("a.txt", "a", 0o644))
	require.NoError(t, untar.ExtractTarGz(archive, filepath.Join(dst, "new"), untar.Options{}))
	assert.FileExists(t, filepath.Join(dst, "new", "a.txt"))
}

func TestExtractTarGz_Overwrites(t *testing.T) {
	base, dst, _ := layout(t)

	require.NoError(t, os.WriteFile(filepath.Join(dst, "a.txt"), []byte("a longer text"), 0o644))

	archive := writeArchive(t, base, file("a.txt", "short", 0o644))
	require.NoError(t, untar.ExtractTarGz(archive, dst, untar.Options{}))

	content, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "short", string(content))
}

func TestExtractTarGz_Unsafe(t *testing.T) {
	cases := []struct {
		name    string
		entries []entry
		err     error
	}{
		{"parent", []entry{file("../x", "x", 0o644)}, untar.ErrUnsafePath},
		{"absolute", []entry{file("/x", "x", 0o644)}, untar.ErrUnsafePath},
		{"cleaned to parent", []entry{file("a/../../x", "x", 0o644)}, untar.ErrUnsafePath},
		{"parent directory", []entry{dir("../x/")}, untar.ErrUnsafePath},
		{"parent symlink name", []entry{symlink("../x", "y")}, untar.ErrUnsafePath},
		{
			"symlink out and a file through it",
			[]entry{symlink("evil", "../outside"), file("evil/x", "x", 0o644)},
			untar.ErrUnsafeLink,
		},
		{
			"nested symlink out",
			[]entry{dir("a/"), file("a/f", "f", 0o644), symlink("a/evil", "../../outside")},
			untar.ErrUnsafeLink,
		},
		{"absolute symlink", []entry{symlink("abs", "/etc")}, untar.ErrUnsafeLink},
		{
			"symlink out through a link to the directory",
			[]entry{symlink("d", "."), symlink("d/l", "../outside")},
			untar.ErrUnsafeLink,
		},
		{
			"symlink out through a link to the directory and its parent",
			[]entry{symlink("d", "."), symlink("x", "d/../outside")},
			untar.ErrUnsafeLink,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			base, dst, outside := layout(t)

			archive := writeArchive(t, base, testCase.entries...)
			err := untar.ExtractTarGz(archive, dst, untar.Options{Symlinks: true, EntryModes: true})
			require.ErrorIs(t, err, testCase.err)

			written, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Empty(t, written, "nothing is written outside the destination")
			assert.NoFileExists(t, filepath.Join(base, "x"))
		})
	}
}

// TestExtractTarGz_NoWriteThroughLinks checks that a file is not written
// through a link out of the destination, one that was there before the
// archive or one an archive makes through a link to its own directory.
func TestExtractTarGz_NoWriteThroughLinks(t *testing.T) {
	t.Run("existing link", func(t *testing.T) {
		base, dst, outside := layout(t)
		require.NoError(t, os.Symlink(outside, filepath.Join(dst, "out")))

		archive := writeArchive(t, base, file("out/x", "x", 0o644))
		err := untar.ExtractTarGz(archive, dst, untar.Options{Symlinks: true, EntryModes: true})
		require.ErrorContains(t, err, "cannot create an extracted directory")
		assert.NoFileExists(t, filepath.Join(outside, "x"))
	})

	t.Run("archive link", func(t *testing.T) {
		base, dst, outside := layout(t)

		archive := writeArchive(t, base,
			symlink("d", "."), symlink("d/l", "../outside"), file("l/x", "x", 0o644))
		err := untar.ExtractTarGz(archive, dst, untar.Options{Symlinks: true, EntryModes: true})
		require.ErrorContains(t, err, "cannot create an extracted directory")
		assert.NoFileExists(t, filepath.Join(outside, "x"))
	})
}

func TestExtractTarGz_UnknownTypes(t *testing.T) {
	cases := []struct {
		name  string
		entry entry
		opts  untar.Options
	}{
		{"symlink not taken", symlink("l", "f"), untar.Options{}},
		{"hard link", entry{"h", tar.TypeLink, 0o644, "", "f"}, untar.Options{Symlinks: true}},
		{"fifo", entry{"p", tar.TypeFifo, 0o644, "", ""}, untar.Options{Symlinks: true}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			base, dst, _ := layout(t)

			archive := writeArchive(t, base, file("f", "f", 0o644), testCase.entry)
			err := untar.ExtractTarGz(archive, dst, testCase.opts)
			require.ErrorIs(t, err, untar.ErrUnknownType)
			require.ErrorContains(t, err, " in "+testCase.entry.name)
		})
	}
}

func TestExtractTarGz_BadArchive(t *testing.T) {
	base, dst, _ := layout(t)

	require.ErrorContains(t, untar.ExtractTarGz(filepath.Join(base, "missing.tgz"), dst,
		untar.Options{}), "cannot open the archive")

	text := filepath.Join(base, "text.tgz")
	require.NoError(t, os.WriteFile(text, []byte("text"), 0o644))
	require.ErrorContains(t, untar.ExtractTarGz(text, dst, untar.Options{}), "cannot decompress")
}
