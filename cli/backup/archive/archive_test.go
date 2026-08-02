package archive

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedFiles is a deterministic input set with explicit expected results.
var fixedFiles = []struct {
	name    string
	content []byte
}{
	{"00000000000000000001.snap", []byte("snap-payload-0123456789")},
	{"00000000000000000002.xlog", []byte("xlog")},
	{"instance_backup.json", []byte(`{"schema_version":1}`)},
}

func writeFixture(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, content, 0o644))
	return path
}

func writeAllFixtures(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, len(fixedFiles))
	for i, f := range fixedFiles {
		paths[i] = writeFixture(t, dir, f.name, f.content)
	}
	return paths
}

// readArchiveRaw returns name->content read with the standard zstd+tar readers.
func readArchiveRaw(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	zr, err := zstd.NewReader(f)
	require.NoError(t, err)
	defer zr.Close()

	tr := tar.NewReader(zr)
	got := map[string][]byte{}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		got[header.Name] = content
	}
	return got
}

func TestPack(t *testing.T) {
	paths := writeAllFixtures(t)
	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")

	err := Pack(archivePath, paths, 3)
	require.NoError(t, err)

	want := map[string][]byte{
		"00000000000000000001.snap": []byte("snap-payload-0123456789"),
		"00000000000000000002.xlog": []byte("xlog"),
		"instance_backup.json":      []byte(`{"schema_version":1}`),
	}
	got := readArchiveRaw(t, archivePath)
	assert.Equal(t, want, got)
}

// readArchiveNames returns entry names in archive order.
func readArchiveNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	zr, err := zstd.NewReader(f)
	require.NoError(t, err)
	defer zr.Close()

	tr := tar.NewReader(zr)
	var names []string
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		names = append(names, header.Name)
	}
	return names
}

func TestPackStoresBaseNameOnly(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	path := writeFixture(t, nested, "00000000000000000001.snap", []byte("x"))

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, []string{path}, 3))

	got := readArchiveRaw(t, archivePath)
	want := map[string][]byte{"00000000000000000001.snap": []byte("x")}
	assert.Equal(t, want, got)
}

func TestPackMissingFile(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	err := Pack(archivePath, []string{filepath.Join(t.TempDir(), "nope.snap")}, 3)
	assert.Error(t, err)

	// A failed pack must not leave a partial archive behind; a leftover valid
	// but incomplete .tar.zst could be silently restored with missing data.
	_, statErr := os.Stat(archivePath)
	assert.True(t, os.IsNotExist(statErr), "partial archive must be removed on error")
}

func TestPackRejectsDuplicateBaseNames(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	require.NoError(t, os.MkdirAll(a, 0o755))
	require.NoError(t, os.MkdirAll(b, 0o755))
	// Same base name in two different directories would flatten to one entry.
	p1 := writeFixture(t, a, "00000000000000000001.snap", []byte("x"))
	p2 := writeFixture(t, b, "00000000000000000001.snap", []byte("y"))

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	err := Pack(archivePath, []string{p1, p2}, 3)
	assert.Error(t, err)

	_, statErr := os.Stat(archivePath)
	assert.True(t, os.IsNotExist(statErr), "archive must not be created on collision")
}

func TestSortWalFilesOrdersByLSN(t *testing.T) {
	// A multi-snapshot backup must be ordered by LSN, not all-snaps-then-all-
	// xlogs: an older WAL (xlog 150) has to precede a newer snapshot (snap 200).
	// A "snaps first" ordering would emit snap 200 before xlog 100/150, which a
	// linear restore-apply would replay in the wrong order. Same-LSN snap and
	// xlog order snap-before-xlog naturally (".snap" < ".xlog").
	files := []string{
		"/a/00000000000000000200.xlog",
		"/b/00000000000000000100.snap",
		"/c/00000000000000000200.snap",
		"/d/00000000000000000100.xlog",
		"/e/00000000000000000150.xlog",
	}
	sortWalFiles(files)
	want := []string{
		"/b/00000000000000000100.snap",
		"/d/00000000000000000100.xlog",
		"/e/00000000000000000150.xlog",
		"/c/00000000000000000200.snap",
		"/a/00000000000000000200.xlog",
	}
	assert.Equal(t, want, files)
}

func TestPackOrdersNonWalFileLast(t *testing.T) {
	dir := t.TempDir()
	// End-to-end: a non-wal file must land after every snap/xlog regardless of
	// its base name and of the input order.
	paths := []string{
		writeFixture(t, dir, "00000000000000000002.xlog", []byte("b")),
		writeFixture(t, dir, "0000_manifest.json", []byte("m")),
		writeFixture(t, dir, "00000000000000000001.snap", []byte("a")),
	}

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, paths, 3))

	want := []string{
		"00000000000000000001.snap",
		"00000000000000000002.xlog",
		"0000_manifest.json",
	}
	assert.Equal(t, want, readArchiveNames(t, archivePath))
}

// rawEntry is a tar entry written verbatim, bypassing Pack.
type rawEntry struct {
	header tar.Header
	body   []byte
}

// writeRawArchive builds a .tar.zst from entries written exactly as given (no
// base-name flattening, any type flag), for exercising the reader-side guards
// against archives tt did not produce.
func writeRawArchive(t *testing.T, path string, entries []rawEntry) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	zw, err := zstd.NewWriter(f)
	require.NoError(t, err)
	tw := tar.NewWriter(zw)

	for _, entry := range entries {
		require.NoError(t, tw.WriteHeader(&entry.header))
		_, err = tw.Write(entry.body)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
}

// writeMaliciousArchive builds a .tar.zst with a single regular entry whose
// stored name is exactly entryName (no base-name flattening), for exercising
// the extraction path-traversal guard.
func writeMaliciousArchive(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	writeRawArchive(t, path, []rawEntry{{
		header: tar.Header{
			Name:     entryName,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		},
		body: content,
	}})
}

func TestUnpackRejectsTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "evil.tar.zst")
	writeMaliciousArchive(t, archivePath, "../escape.snap", []byte("pwn"))

	destDir := t.TempDir()
	err := Unpack(archivePath, destDir)
	assert.Error(t, err, "traversal entry must be rejected")

	// Nothing must be written outside destDir.
	_, statErr := os.Stat(filepath.Join(filepath.Dir(destDir), "escape.snap"))
	assert.True(t, os.IsNotExist(statErr), "no file may be written outside destDir")
}

func TestEntriesRejectsTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "evil.tar.zst")
	writeMaliciousArchive(t, archivePath, "../../escape.snap", []byte("pwn"))

	var gotErr error
	for _, err := range Entries(archivePath) {
		if err != nil {
			gotErr = err
		}
	}
	assert.Error(t, gotErr, "traversal entry must surface an error")
}

// unsafeEntryNames are names Pack can never produce: it stores base names only,
// so anything carrying a directory component is hostile or corrupt input. A
// consumer creating the parent directories of such a name writes outside the
// destination as soon as one component is a pre-existing symlink.
var unsafeEntryNames = []string{
	"sub/evil.snap",
	"./name.snap",
	"a/b/c.xlog",
	"/etc/passwd",
}

func TestUnpackRejectsEntryNameWithPathSeparator(t *testing.T) {
	for _, name := range unsafeEntryNames {
		t.Run(name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "evil.tar.zst")
			writeMaliciousArchive(t, archivePath, name, []byte("pwn"))

			destDir := t.TempDir()
			assert.Error(t, Unpack(archivePath, destDir), "entry %q must be rejected", name)

			entries, err := os.ReadDir(destDir)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing may be written for a rejected entry")
		})
	}
}

func TestEntriesRejectsEntryNameWithPathSeparator(t *testing.T) {
	for _, name := range unsafeEntryNames {
		t.Run(name, func(t *testing.T) {
			archivePath := filepath.Join(t.TempDir(), "evil.tar.zst")
			writeMaliciousArchive(t, archivePath, name, []byte("pwn"))

			var gotNames []string
			var gotErr error
			for entry, err := range Entries(archivePath) {
				if err != nil {
					gotErr = err
					break
				}
				gotNames = append(gotNames, entry.Name)
			}
			assert.Error(t, gotErr, "entry %q must surface an error", name)
			assert.Empty(t, gotNames, "a rejected entry must not be yielded")
		})
	}
}

// TestEntriesRejectsNonRegularEntry checks the readers agree on a hostile
// archive: a symlink entry must not be dropped silently, or a restore reports
// success with a journal missing.
func TestEntriesRejectsNonRegularEntry(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "symlink.tar.zst")
	writeRawArchive(t, archivePath, []rawEntry{
		{
			header: tar.Header{
				Name:     "00000000000000000000.snap",
				Mode:     0o644,
				Size:     4,
				Typeflag: tar.TypeReg,
			},
			body: []byte("snap"),
		},
		{
			header: tar.Header{
				Name:     "00000000000000000000.xlog",
				Mode:     0o777,
				Typeflag: tar.TypeSymlink,
				Linkname: "/etc/passwd",
			},
		},
	})

	var gotNames []string
	var gotErr error
	for entry, err := range Entries(archivePath) {
		if err != nil {
			gotErr = err
			break
		}
		gotNames = append(gotNames, entry.Name)
	}
	require.Error(t, gotErr, "a non-regular entry must not be skipped silently")
	assert.Contains(t, gotErr.Error(), "00000000000000000000.xlog")
	assert.Equal(t, []string{"00000000000000000000.snap"}, gotNames)

	assert.Error(t, Unpack(archivePath, t.TempDir()), "Unpack must reject the same archive")
}

// TestReadersSkipDirectoryEntry pins the one non-regular type both readers
// ignore instead of rejecting: a directory entry carries no content, and any
// file under it is rejected by the entry-name guard anyway.
func TestReadersSkipDirectoryEntry(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "withdir.tar.zst")
	writeRawArchive(t, archivePath, []rawEntry{
		{header: tar.Header{Name: "sub/", Mode: 0o755, Typeflag: tar.TypeDir}},
		{
			header: tar.Header{
				Name:     "00000000000000000000.snap",
				Mode:     0o644,
				Size:     4,
				Typeflag: tar.TypeReg,
			},
			body: []byte("snap"),
		},
	})

	var gotNames []string
	for entry, err := range Entries(archivePath) {
		require.NoError(t, err)
		gotNames = append(gotNames, entry.Name)
	}
	assert.Equal(t, []string{"00000000000000000000.snap"}, gotNames)

	destDir := t.TempDir()
	require.NoError(t, Unpack(archivePath, destDir))
	entries, err := os.ReadDir(destDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "00000000000000000000.snap", entries[0].Name())
}

func TestPackOrdersByLSN(t *testing.T) {
	dir := t.TempDir()
	// Shuffled input across LSNs; snaps and xlogs must interleave by LSN, not
	// group all snaps before all xlogs (snap 3 must follow xlog 2, not precede).
	paths := []string{
		writeFixture(t, dir, "00000000000000000005.xlog", []byte("e")),
		writeFixture(t, dir, "00000000000000000002.xlog", []byte("b")),
		writeFixture(t, dir, "00000000000000000003.snap", []byte("c")),
		writeFixture(t, dir, "00000000000000000001.snap", []byte("a")),
	}

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, paths, 3))

	want := []string{
		"00000000000000000001.snap",
		"00000000000000000002.xlog",
		"00000000000000000003.snap",
		"00000000000000000005.xlog",
	}
	assert.Equal(t, want, readArchiveNames(t, archivePath))
}

func TestPackDoesNotMutateInput(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		writeFixture(t, dir, "00000000000000000002.xlog", []byte("b")),
		writeFixture(t, dir, "00000000000000000001.snap", []byte("a")),
	}
	original := append([]string(nil), paths...)

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, paths, 3))

	assert.Equal(t, original, paths)
}

func TestUnpack(t *testing.T) {
	paths := writeAllFixtures(t)
	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, paths, 3))

	destDir := t.TempDir()
	err := Unpack(archivePath, destDir)
	require.NoError(t, err)

	for _, f := range fixedFiles {
		got, err := os.ReadFile(filepath.Join(destDir, f.name))
		require.NoError(t, err, "file %q must be extracted", f.name)
		assert.Equal(t, f.content, got, "content of %q must match", f.name)
	}

	entries, err := os.ReadDir(destDir)
	require.NoError(t, err)
	assert.Len(t, entries, len(fixedFiles))
}

func TestUnpackMissingArchive(t *testing.T) {
	err := Unpack(filepath.Join(t.TempDir(), "nope.tar.zst"), t.TempDir())
	assert.Error(t, err)
}

func TestEntries(t *testing.T) {
	paths := writeAllFixtures(t)
	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, paths, 3))

	type record struct {
		name    string
		size    int64
		content string
	}
	var got []record
	for entry, err := range Entries(archivePath) {
		require.NoError(t, err)
		body, err := io.ReadAll(entry.Body)
		require.NoError(t, err)
		got = append(got, record{entry.Name, entry.Size, string(body)})
	}

	want := []record{
		{"00000000000000000001.snap", 23, "snap-payload-0123456789"},
		{"00000000000000000002.xlog", 4, "xlog"},
		{"instance_backup.json", 20, `{"schema_version":1}`},
	}
	assert.Equal(t, want, got)
}

func TestEntriesEarlyStop(t *testing.T) {
	paths := writeAllFixtures(t)
	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, paths, 3))

	// Breaking out of the loop must stop cleanly without panicking.
	var names []string
	for entry, err := range Entries(archivePath) {
		require.NoError(t, err)
		names = append(names, entry.Name)
		break
	}
	assert.Equal(t, []string{"00000000000000000001.snap"}, names)
}

func TestEntriesMissingArchive(t *testing.T) {
	var gotErr error
	for _, err := range Entries(filepath.Join(t.TempDir(), "nope.tar.zst")) {
		gotErr = err
	}
	assert.Error(t, gotErr)
}

func TestChecksum(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{
			name:    "empty",
			content: []byte(""),
			want:    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name:    "hello",
			content: []byte("hello archive"),
			want:    "f8976760708ac1d60ab4b2dd1fa3c02d3bbf9693846f1db27aa77b46f0bb4276",
		},
		{
			name:    "snap-payload",
			content: []byte("snap-payload-0123456789"),
			want:    "7c97d53b9032e28571341d80202850104a6b87da3c0a8c9f2c93c497cc38248a",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFixture(t, dir, tc.name, tc.content)
			got, err := Checksum(path)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestChecksumMissingFile(t *testing.T) {
	_, err := Checksum(filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err)
}

// TestChecksumStable checks the sum depends only on content, not on path.
func TestChecksumStable(t *testing.T) {
	content := []byte("stable-content")
	a := writeFixture(t, t.TempDir(), "a", content)
	b := writeFixture(t, t.TempDir(), "b", content)

	sumA, err := Checksum(a)
	require.NoError(t, err)
	sumB, err := Checksum(b)
	require.NoError(t, err)
	assert.Equal(t, sumA, sumB)
}

// TestPackUnpackLargeFile checks a large file round-trips intact.
func TestPackUnpackLargeFile(t *testing.T) {
	dir := t.TempDir()
	large := bytes.Repeat([]byte("0123456789abcdef"), 1<<20) // 16 MiB
	path := writeFixture(t, dir, "00000000000000000001.snap", large)

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, []string{path}, 3))

	destDir := t.TempDir()
	require.NoError(t, Unpack(archivePath, destDir))

	got, err := os.ReadFile(filepath.Join(destDir, "00000000000000000001.snap"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(large, got), "large file must round-trip intact")
}
