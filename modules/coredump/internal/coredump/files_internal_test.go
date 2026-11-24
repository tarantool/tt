package coredump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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

	// The files and the directories are their user's alone, whatever the
	// modes of the entries give others.
	for name, mode := range map[string]os.FileMode{
		"core/bin/run.sh": 0o700, "core/data/version": 0o600, "top.txt": 0o600, "core": 0o700,
		"core/bin": 0o700, "core/data": 0o700,
	} {
		info, err := os.Stat(filepath.Join(dst, name))
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode().Perm(), name)
	}

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

	// The file overwritten gets the mode of the entry, of its owner alone,
	// rather than keep the one others could read it by.
	info, err := os.Stat(filepath.Join(dst, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
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

// fragment is the data of a sparse file of a test archive at its offset.
type fragment struct {
	offset int64
	data   string
}

// tarBlockSize is the size of a block of a tar archive.
const tarBlockSize = 512

// tarHeader returns the ustar header block of an entry name of the type
// typeflag and size bytes of data.
func tarHeader(name string, typeflag byte, size int) []byte {
	block := make([]byte, tarBlockSize)
	copy(block[0:100], name)
	copy(block[100:], "0000644\x00")
	copy(block[108:], "0000000\x00")
	copy(block[116:], "0000000\x00")
	copy(block[124:], fmt.Sprintf("%011o\x00", size))
	copy(block[136:], "00000000000\x00")
	copy(block[148:156], "        ")
	copy(block[257:], "ustar\x0000")

	block[156] = typeflag

	sum := 0
	for _, b := range block {
		sum += int(b)
	}

	copy(block[148:], fmt.Sprintf("%06o\x00 ", sum))

	return block
}

// padded returns data padded with zeros to a whole number of tar blocks.
func padded(data []byte) []byte {
	return append(data, make([]byte, (tarBlockSize-len(data)%tarBlockSize)%tarBlockSize)...)
}

// paxRecord returns the record of key and value of an extended header of
// PAX, "<length> <key>=<value>\n", the length counting itself.
func paxRecord(key, value string) string {
	body := len(key) + len(value) + len(" =\n")
	length := body + len(strconv.Itoa(body))

	record := fmt.Sprintf("%d %s=%s\n", length, key, value)
	if len(record) != length {
		record = fmt.Sprintf("%d %s=%s\n", len(record), key, value)
	}

	return record
}

// writeSparseArchive writes a tar.gz archive into dir of one file, name, of
// size bytes, the fragments and holes elsewhere, as tar --sparse
// --format=posix of pack stores the core: an extended header of the
// GNU.sparse records of the format 1.0, and an entry whose data is the map
// of the fragments, the number of them and the offset and the size of each,
// in whole blocks, and the fragments after it. archive/tar writes no sparse
// entry.
func writeSparseArchive(t *testing.T, dir, name string, size int64,
	fragments []fragment,
) string {
	t.Helper()

	records := paxRecord("GNU.sparse.major", "1") + paxRecord("GNU.sparse.minor", "0") +
		paxRecord("GNU.sparse.name", name) +
		paxRecord("GNU.sparse.realsize", strconv.FormatInt(size, 10))

	var sparseMap, data bytes.Buffer

	fmt.Fprintf(&sparseMap, "%d\n", len(fragments))

	for _, item := range fragments {
		fmt.Fprintf(&sparseMap, "%d\n%d\n", item.offset, len(item.data))
		data.WriteString(item.data)
	}

	content := append(padded(sparseMap.Bytes()), data.Bytes()...)

	var stream bytes.Buffer

	stream.Write(tarHeader("PaxHeaders/"+name, tar.TypeXHeader, len(records)))
	stream.Write(padded([]byte(records)))
	stream.Write(tarHeader("GNUSparseFile.0/"+name, tar.TypeReg, len(content)))
	stream.Write(padded(content))
	stream.Write(make([]byte, 2*tarBlockSize))

	path := filepath.Join(dir, "sparse.tar.gz")

	var compressed bytes.Buffer

	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write(stream.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, os.WriteFile(path, compressed.Bytes(), 0o644))

	return path
}

// diskSize returns the bytes the file at path takes on the disk.
func diskSize(t *testing.T, path string) int64 {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)

	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok, "the blocks of %s are told", path)

	return stat.Blocks * 512
}

// keepsHoles reports whether the file system of dir keeps a hole of gap
// bytes between two blocks of data, of a file written as writeSparse
// writes one: a block, a seek over the gap from its end, and a block; the
// file is kept sparse when it takes less of the disk than its size. The
// file systems of Linux keep a hole of a block, ext4, XFS, Btrfs and tmpfs
// among them, and there one that does not fails the test, so that the
// check of the holes is never dropped unseen. Elsewhere it is the file
// system's own: the APFS of the macOS runner of CI allocated the zeros of a
// gap of 12 KiB and of one of a MiB, and kept a hole of 16 MiB, so the
// probe is of the gap the test writes, and where it is not kept, the test
// passes over its check of the holes and says so.
func keepsHoles(t *testing.T, dir string, gap int64) bool {
	t.Helper()

	path := filepath.Join(dir, "holes")
	block := bytes.Repeat([]byte{1}, sparseBlock)

	probe, err := os.Create(path)
	require.NoError(t, err)

	_, err = probe.Write(block)
	require.NoError(t, err)

	_, err = probe.Seek(gap, io.SeekCurrent)
	require.NoError(t, err)

	_, err = probe.Write(block)
	require.NoError(t, err)
	require.NoError(t, probe.Close())

	kept := diskSize(t, path) < gap+2*sparseBlock
	require.NoError(t, os.Remove(path))

	if kept {
		return true
	}

	if runtime.GOOS == "linux" {
		t.Fatalf("the file system of %s keeps no hole of %d bytes: those of Linux do, and "+
			"the holes are checked there", dir, gap)
	}

	t.Logf("the file system of %s keeps no hole of %d bytes: the holes are not checked",
		dir, gap)

	return false
}

// TestExtractTarGzSparse checks that a file is extracted with its holes: of
// a sparse entry, as pack stores the core, and the zeros of a regular one.
// The content is the same, and the file takes the blocks of its data alone
// on the disk, a hole at its end too, where the file system keeps a hole of
// the gap between the data (see keepsHoles). archive/tar reads the holes
// back as zeros, which were written out: a core of 11 MB on the disk and
// 1.1 GB in size took 1.1 GB.
func TestExtractTarGzSparse(t *testing.T) {
	t.Parallel()

	const size = 32 << 20

	fragments := []fragment{
		{offset: 0, data: "\x7fELF" + strings.Repeat("h", 1000)},
		{offset: size/2 + 100, data: "the data in the middle"},
	}

	expected := make([]byte, size)
	for _, item := range fragments {
		copy(expected[item.offset:], item.data)
	}

	cases := []struct {
		name    string
		archive func(dir string) string
	}{
		{"sparse entry", func(dir string) string {
			return writeSparseArchive(t, dir, "core", size, fragments)
		}},
		{"zeros of a regular entry", func(dir string) string {
			return writeArchive(t, dir, file("core", string(expected), 0o600))
		}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			base, dst, _ := layout(t)

			require.NoError(t, extractTarGz(testCase.archive(base), dst))

			path := filepath.Join(dst, "core")
			content, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Len(t, content, size)
			assert.True(t, bytes.Equal(expected, content), "the content of the file")

			// The blocks of zeros between the fragments are the half of the
			// file but one block.
			if keepsHoles(t, dst, size/2-sparseBlock) {
				assert.Less(t, diskSize(t, path), int64(1<<20),
					"the holes of the file take no blocks")
			}
		})
	}
}

// TestExtractTarGzTruncated checks that a file cut short in the archive is
// an error, not a file extracted short: the reader of an entry reports it
// with io.ErrUnexpectedEOF, which io.ReadFull reports at the end of any
// entry that does not fill its buffer.
func TestExtractTarGzTruncated(t *testing.T) {
	t.Parallel()

	base, dst, _ := layout(t)

	archive := writeArchive(t, base, file("core", strings.Repeat("x", 10000), 0o600))

	content, err := os.ReadFile(archive)
	require.NoError(t, err)

	stream, err := gzip.NewReader(bytes.NewReader(content))
	require.NoError(t, err)

	raw, err := io.ReadAll(stream)
	require.NoError(t, err)

	var cut bytes.Buffer

	writer := gzip.NewWriter(&cut)

	_, err = writer.Write(raw[:tarBlockSize+5000])
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	truncated := filepath.Join(base, "truncated.tar.gz")
	require.NoError(t, os.WriteFile(truncated, cut.Bytes(), 0o644))

	err = extractTarGz(truncated, dst)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.ErrorContains(t, err, `cannot extract "core"`)
}

// TestUnpackedDir checks that the artefacts of an archive are in the one
// directory it holds, whatever the archive is named, and in the directory
// it is extracted into otherwise.
func TestUnpackedDir(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		entries []entry
		want    string
	}{
		{
			"the directory of pack", []entry{
				file("tarantool-core-N-202611240156-host/version", "Tarantool 3.9.0", 0o644),
				file("tarantool-core-N-202611240156-host/ext/x.py", "x", 0o644),
			}, "tarantool-core-N-202611240156-host",
		},
		{
			"the artefacts at the root", []entry{
				file("version", "Tarantool 3.9.0", 0o644), file("ext/x.py", "x", 0o644),
			}, ".",
		},
		{"a file alone", []entry{file("version", "Tarantool 3.9.0", 0o644)}, "."},
		{"nothing", nil, "."},
	}

	for _, testCase := range cases {
		base, dst, _ := layout(t)

		require.NoError(t, extractTarGz(writeArchive(t, base, testCase.entries...), dst),
			testCase.name)

		unpacked, err := unpackedDir(dst)
		require.NoError(t, err, testCase.name)
		assert.Equal(t, testCase.want, unpacked, testCase.name)
	}

	_, err := unpackedDir(filepath.Join(t.TempDir(), "missing"))
	require.ErrorContains(t, err, "cannot read the unpacked archive")
}
