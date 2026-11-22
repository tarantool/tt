package coredump

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCopyEmbedded(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{"scripts/gdb.sh": &fstest.MapFile{Data: []byte("#!/bin/sh\n")}}
	dst := filepath.Join(t.TempDir(), "gdb.sh")

	require.NoError(t, copyEmbedded(fsys, "scripts/gdb.sh", dst, 0o755))

	content, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n", string(content))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, createdMode(t, 0o755), info.Mode().Perm())

	require.ErrorContains(t, copyEmbedded(fsys, "scripts/missing.sh", dst, 0o755),
		"cannot read the source file")
	require.ErrorContains(t, copyEmbedded(fsys, "scripts/gdb.sh",
		filepath.Join(t.TempDir(), "missing", "gdb.sh"), 0o755),
		"cannot write the destination file")
}

func TestExtractTarGzWellFormed(t *testing.T) {
	t.Parallel()

	base, dst, _ := layout(t)

	archive := writeArchive(t, base,
		dir("./"),
		file("./core/bin/run.sh", "#!/bin/sh\n", 0o755),
		dir("./core/"),
		file("core/data/version", "Tarantool 3.9.0\n", 0o640),
		file("top.txt", "top", 0o644),
		symlink("core/bin/run", "run.sh"),
		symlink("core/link-to-data", "data/../data/version"),
		symlink("core/dangling", "missing"),
	)

	require.NoError(t, extractTarGz(archive, dst))

	content, err := os.ReadFile(filepath.Join(dst, "core", "bin", "run"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\n", string(content))

	info, err := os.Stat(filepath.Join(dst, "core", "bin", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, createdMode(t, 0o755), info.Mode().Perm())

	info, err = os.Stat(filepath.Join(dst, "core", "data", "version"))
	require.NoError(t, err)
	assert.Equal(t, createdMode(t, 0o640), info.Mode().Perm())

	target, err := os.Readlink(filepath.Join(dst, "core", "link-to-data"))
	require.NoError(t, err)
	assert.Equal(t, "data/../data/version", target)

	target, err = os.Readlink(filepath.Join(dst, "core", "dangling"))
	require.NoError(t, err)
	assert.Equal(t, "missing", target)

	assert.FileExists(t, filepath.Join(dst, "top.txt"))
}

func TestExtractTarGzCreatesAndOverwrites(t *testing.T) {
	t.Parallel()

	base, dst, _ := layout(t)

	archive := writeArchive(t, base, file("a.txt", "short", 0o644))
	require.NoError(t, extractTarGz(archive, filepath.Join(dst, "new")))
	assert.FileExists(t, filepath.Join(dst, "new", "a.txt"))

	require.NoError(t, os.WriteFile(filepath.Join(dst, "a.txt"), []byte("a longer text"), 0o644))
	require.NoError(t, extractTarGz(archive, dst))

	content, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "short", string(content))
}

func TestExtractTarGzUnsafe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		entries []entry
		err     error
	}{
		{"parent", []entry{file("../x", "x", 0o644)}, errUnsafePath},
		{"absolute", []entry{file("/x", "x", 0o644)}, errUnsafePath},
		{"cleaned to parent", []entry{file("a/../../x", "x", 0o644)}, errUnsafePath},
		{"parent symlink name", []entry{symlink("../x", "y")}, errUnsafePath},
		{
			"symlink out and a file through it",
			[]entry{symlink("evil", "../outside"), file("evil/x", "x", 0o644)},
			errUnsafeLink,
		},
		{"absolute symlink", []entry{symlink("abs", "/etc")}, errUnsafeLink},
		{
			"symlink out through a link to the directory",
			[]entry{symlink("d", "."), symlink("d/l", "../outside")},
			errUnsafeLink,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			base, dst, outside := layout(t)

			archive := writeArchive(t, base, testCase.entries...)
			require.ErrorIs(t, extractTarGz(archive, dst), testCase.err)

			written, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Empty(t, written, "nothing is written outside the destination")
			assert.NoFileExists(t, filepath.Join(base, "x"))
		})
	}
}

func TestExtractTarGzNoWriteThroughLinks(t *testing.T) {
	t.Parallel()

	base, dst, outside := layout(t)
	require.NoError(t, os.Symlink(outside, filepath.Join(dst, "out")))

	archive := writeArchive(t, base, file("out/x", "x", 0o644))
	require.ErrorContains(t, extractTarGz(archive, dst), "cannot create an extracted directory")
	assert.NoFileExists(t, filepath.Join(outside, "x"))
}

func TestExtractTarGzUnknownTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		entry entry
	}{
		{"hard link", entry{"h", tar.TypeLink, 0o644, "", "f"}},
		{"fifo", entry{"p", tar.TypeFifo, 0o644, "", ""}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			base, dst, _ := layout(t)

			archive := writeArchive(t, base, file("f", "f", 0o644), testCase.entry)
			err := extractTarGz(archive, dst)
			require.ErrorIs(t, err, errUnknownType)
			require.ErrorContains(t, err, " in "+testCase.entry.name)
		})
	}
}

func TestExtractTarGzBadArchive(t *testing.T) {
	t.Parallel()

	base, dst, _ := layout(t)

	require.ErrorContains(t, extractTarGz(filepath.Join(base, "missing.tgz"), dst),
		"cannot open the archive")

	text := filepath.Join(base, "text.tgz")
	require.NoError(t, os.WriteFile(text, []byte("text"), 0o644))
	require.ErrorContains(t, extractTarGz(text, dst), "cannot decompress")
}
