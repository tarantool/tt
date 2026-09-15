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

// TestPackStoresBaseNameOnlyWithoutRoots checks that a Pack call with no roots
// (the wal/xlog case: those files sit flat in their data directory, so there is
// nothing to preserve) still flattens to the base name, same as before roots
// existed.
func TestPackStoresBaseNameOnlyWithoutRoots(t *testing.T) {
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

// TestPackWithRootsPreservesNestedPath checks that a file under its own data
// directory keeps its subdirectory structure inside the archive -- the shape
// vinyl's .run/.index files need, since they live under
// <vinyl_dir>/<space_id>/<index_id>/, not flat like snap/xlog.
func TestPackWithRootsPreservesNestedPath(t *testing.T) {
	vinylDir := t.TempDir()
	nested := filepath.Join(vinylDir, "512", "0")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	path := writeFixture(t, nested, "00000000000000000001.run", []byte("run-data"))

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, []string{path}, 3, DataDirs{Vinyl: vinylDir}))

	got := readArchiveRaw(t, archivePath)
	want := map[string][]byte{"512/0/00000000000000000001.run": []byte("run-data")}
	assert.Equal(t, want, got)
}

// TestEntryName checks that a file is named relative to the data directory of
// its own kind, and falls back to its base name when that directory is unknown
// or does not hold it.
func TestEntryName(t *testing.T) {
	const (
		snap     = "00000000000000000001.snap"
		sortData = "00000000000000000001.sortdata"
		xlog     = "00000000000000000001.xlog"
		vylog    = "00000000000000000001.vylog"
		runBase  = "00000000000000000001.run"
		run      = "512/0/" + runBase
	)

	flat := DataDirs{WAL: "/data", Memtx: "/data", Vinyl: "/data"}
	split := DataDirs{WAL: "/data/wal", Memtx: "/data/memtx", Vinyl: "/data/vinyl"}
	// vinyl_dir and memtx_dir nested inside wal_dir: a prefix match against
	// wal_dir must not win over the directory of the file's own kind.
	nested := DataDirs{WAL: "/data", Memtx: "/data/memtx", Vinyl: "/data/vinyl"}
	// wal_dir and memtx_dir nested inside vinyl_dir, the layout that tells a
	// file named against its own directory from one named against the vinyl
	// directory it happens to sit under: the second keeps a directory in front
	// of the name, and the base-name fallback is never reached.
	underVinyl := DataDirs{
		WAL:   "/data/vinyl/wal",
		Memtx: "/data/vinyl/memtx",
		Vinyl: "/data/vinyl",
	}

	tests := []struct {
		name string
		file string
		dirs DataDirs
		want string
	}{
		{"no dirs at all", "/data/wal/" + snap, DataDirs{}, snap},
		{"flat snap", "/data/" + snap, flat, snap},
		{"flat xlog", "/data/" + xlog, flat, xlog},
		{"flat vylog", "/data/" + vylog, flat, vylog},
		{"flat run", "/data/" + run, flat, run},
		{"split snap", "/data/memtx/" + snap, split, snap},
		{"split xlog", "/data/wal/" + xlog, split, xlog},
		{"split run", "/data/vinyl/" + run, split, run},
		{"nested memtx snap", "/data/memtx/" + snap, nested, snap},
		{"nested vinyl run", "/data/vinyl/" + run, nested, run},
		{"nested xlog", "/data/" + xlog, nested, xlog},
		// Sort data is written beside the snapshot it describes, so it is
		// named against memtx_dir like the snapshot.
		{"split sort data", "/data/memtx/" + sortData, split, sortData},
		{"nested sort data", "/data/memtx/" + sortData, nested, sortData},
		{
			"sort data with memtx_dir under vinyl_dir",
			"/data/vinyl/memtx/" + sortData,
			underVinyl,
			sortData,
		},
		{
			"a snapshot with memtx_dir under vinyl_dir",
			"/data/vinyl/memtx/" + snap,
			underVinyl,
			snap,
		},
		// A file Tarantool was still writing is named against the directory
		// its finished form lives in: a restore strips the suffix before it
		// routes the entry, and the two have to agree.
		{
			"an open journal",
			"/data/wal/" + xlog + ".inprogress",
			split,
			xlog + ".inprogress",
		},
		{
			"an open journal with wal_dir under vinyl_dir",
			"/data/vinyl/wal/" + xlog + ".inprogress",
			underVinyl,
			xlog + ".inprogress",
		},
		{
			"an open snapshot with memtx_dir under vinyl_dir",
			"/data/vinyl/memtx/" + snap + ".inprogress",
			underVinyl,
			snap + ".inprogress",
		},
		{
			"an open snapshot",
			"/data/memtx/" + snap + ".inprogress",
			split,
			snap + ".inprogress",
		},
		// A file outside the directory of its own kind keeps only its base
		// name, even when another kind's directory does contain it.
		{"snap outside memtx_dir", "/data/wal/" + snap, split, snap},
		{"run outside vinyl_dir", "/data/wal/" + run, split, runBase},
		// An instance with no vinyl_dir configured.
		{"empty own dir", "/data/vinyl/" + run, DataDirs{WAL: "/data/wal"}, runBase},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, EntryName(filepath.FromSlash(tc.file), tc.dirs))
		})
	}
}

// packLayout is one instance data-directory layout: where each kind of file
// lives, relative to a single base directory.
type packLayout struct {
	name  string
	wal   string
	memtx string
	vinyl string
}

// TestPackNamesEntriesByFileKind packs one file of every kind a backup carries
// -- a snapshot, the sort data beside it, a journal, a journal Tarantool was
// still writing, and a nested vinyl run -- through Pack and reads the entries
// back, over the data-directory layouts an instance can have. Every layout
// must yield the same names: a restore routes an entry to a target directory
// by file kind, so a name taken relative to another kind's directory would
// land the file in the wrong place.
func TestPackNamesEntriesByFileKind(t *testing.T) {
	layouts := []packLayout{
		{
			name:  "all dirs equal",
			wal:   ".",
			memtx: ".",
			vinyl: ".",
		},
		{
			name:  "all dirs distinct",
			wal:   "wal",
			memtx: "memtx",
			vinyl: "vinyl",
		},
		{
			name:  "vinyl_dir nested inside wal_dir",
			wal:   "wal",
			memtx: "memtx",
			vinyl: "wal/vinyl",
		},
		{
			name:  "memtx_dir nested inside wal_dir",
			wal:   "wal",
			memtx: "wal/memtx",
			vinyl: "vinyl",
		},
		{
			// The layout a wrong classification shows up in: a file named
			// against vinyl_dir keeps a directory in front of its name here,
			// instead of falling back on the base name and looking right.
			name:  "wal_dir and memtx_dir nested inside vinyl_dir",
			wal:   "vinyl/wal",
			memtx: "vinyl/memtx",
			vinyl: "vinyl",
		},
	}

	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			base := t.TempDir()
			dirs := DataDirs{
				WAL:   filepath.Join(base, filepath.FromSlash(layout.wal)),
				Memtx: filepath.Join(base, filepath.FromSlash(layout.memtx)),
				Vinyl: filepath.Join(base, filepath.FromSlash(layout.vinyl)),
			}

			runDir := filepath.Join(dirs.Vinyl, "512", "0")
			for _, dir := range []string{dirs.WAL, dirs.Memtx, runDir} {
				require.NoError(t, os.MkdirAll(dir, 0o755))
			}

			files := []string{
				writeFixture(t, dirs.Memtx, "00000000000000000001.snap", []byte("snap")),
				writeFixture(t, dirs.Memtx, "00000000000000000001.sortdata",
					[]byte("sortdata")),
				writeFixture(t, dirs.WAL, "00000000000000000001.xlog", []byte("xlog")),
				writeFixture(t, dirs.WAL, "00000000000000000002.xlog.inprogress",
					[]byte("open")),
				writeFixture(t, runDir, "00000000000000000001.run", []byte("run")),
			}

			archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
			require.NoError(t, Pack(archivePath, files, 3, dirs))

			got := map[string]string{}
			for entry, err := range Entries(archivePath) {
				require.NoError(t, err)
				body, err := io.ReadAll(entry.Body)
				require.NoError(t, err)
				got[entry.Name] = string(body)
			}

			want := map[string]string{
				"00000000000000000001.snap":            "snap",
				"00000000000000000001.sortdata":        "sortdata",
				"00000000000000000001.xlog":            "xlog",
				"00000000000000000002.xlog.inprogress": "open",
				"512/0/00000000000000000001.run":       "run",
			}
			assert.Equal(t, want, got)
		})
	}
}

// TestPackFileOutsideEveryDirUsesBaseName checks a file that lies under none of
// the instance's data directories is stored flat, so it can never be extracted
// outside the directory its kind is restored into.
func TestPackFileOutsideEveryDirUsesBaseName(t *testing.T) {
	stray := t.TempDir()
	nested := filepath.Join(stray, "512", "0")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	path := writeFixture(t, nested, "00000000000000000001.run", []byte("run"))

	dirs := DataDirs{
		WAL:   filepath.Join(t.TempDir(), "wal"),
		Memtx: filepath.Join(t.TempDir(), "memtx"),
		Vinyl: filepath.Join(t.TempDir(), "vinyl"),
	}

	archivePath := filepath.Join(t.TempDir(), "backup.tar.zst")
	require.NoError(t, Pack(archivePath, []string{path}, 3, dirs))

	assert.Equal(t, []string{"00000000000000000001.run"}, readArchiveNames(t, archivePath))
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

// unsafeEntryNames are archive entry names that must always be rejected.
var unsafeEntryNames = []string{
	"./name.snap",
	"/etc/passwd",
	"sub/./evil.snap",
	"sub//evil.snap",
}

func TestUnpackRejectsUnsafeEntryName(t *testing.T) {
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

func TestEntriesRejectsUnsafeEntryName(t *testing.T) {
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

func TestUnpackAcceptsNestedEntryName(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "nested.tar.zst")
	writeMaliciousArchive(t, archivePath, "512/0/00000000000000000001.run", []byte("run-data"))

	destDir := t.TempDir()
	require.NoError(t, Unpack(archivePath, destDir))

	got, err := os.ReadFile(filepath.Join(destDir, "512", "0", "00000000000000000001.run"))
	require.NoError(t, err)
	assert.Equal(t, []byte("run-data"), got)
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
