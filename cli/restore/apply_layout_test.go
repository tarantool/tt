package restore

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-xlog/format"
)

const (
	snapName     = "00000000000000000000.snap"
	sortDataName = "00000000000000000000.sortdata"
	xlogName     = "00000000000000000000.xlog"
	vylogName    = "00000000000000000000.vylog"
	runName      = "512/0/00000000000000000004.run"
	indexName    = "512/0/00000000000000000005.index"
)

// splitLayout returns three directories that do not overlap, none of which
// exists yet -- the shape of a Tarantool 3.x instance that configures
// snapshot.dir, wal.dir and vinyl.dir apart from one another.
func splitLayout(t *testing.T) Layout {
	t.Helper()

	root := t.TempDir()

	return Layout{
		Snapshot: filepath.Join(root, "memtx"),
		WAL:      filepath.Join(root, "wal"),
		Vinyl:    filepath.Join(root, "vinyl"),
	}
}

// mixedArchive packs one archive holding every kind of file a backup carries:
// a snapshot, a journal, the vylog, and a vinyl run/index pair under the
// <space_id>/<index_id>/ prefix the archive names them with.
func mixedArchive(t *testing.T) string {
	t.Helper()

	src := t.TempDir()
	snap := writeSnap(t, src, format.VClock{1: 0})
	xlog := writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2, 3))
	vylog := writeJournal(t, src, format.FiletypeVYLOG, format.VClock{}, nil, nil)
	run := writeJournal(t, src, format.FiletypeRUN, format.VClock{1: 4}, nil, nil)
	index := writeJournal(t, src, format.FiletypeINDEX, format.VClock{1: 5}, nil, nil)

	return packRawArchive(t, filepath.Join(t.TempDir(), "mixed.tar.zst"),
		rawEntry{name: snapName, path: snap},
		rawEntry{name: xlogName, path: xlog},
		rawEntry{name: vylogName, path: vylog},
		rawEntry{name: runName, path: run},
		rawEntry{name: indexName, path: index},
	)
}

// applyMixed unpacks the mixed archive into a layout, stamping the headers.
func applyMixed(t *testing.T, layout Layout) *ApplyResult {
	t.Helper()

	result, err := Apply(ApplyOpts{
		Archives:  []string{mixedArchive(t)},
		Layout:    layout,
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	return result
}

func TestLayout_DirForRoutesByKind(t *testing.T) {
	layout := Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"}

	tests := []struct {
		name  string
		entry string
		want  string
	}{
		{name: "a snapshot", entry: snapName, want: "/memtx"},
		{
			name:  "the sort data of a snapshot goes beside that snapshot",
			entry: sortDataName,
			want:  "/memtx",
		},
		{name: "a journal", entry: xlogName, want: "/wal"},
		{name: "the vylog", entry: vylogName, want: "/vinyl"},
		{name: "a vinyl run", entry: runName, want: "/vinyl"},
		{name: "a vinyl index", entry: indexName, want: "/vinyl"},
		{
			name:  "an open journal goes where its finished form would",
			entry: xlogName + ".inprogress",
			want:  "/wal",
		},
		{
			name:  "an open snapshot goes where its finished form would",
			entry: snapName + ".inprogress",
			want:  "/memtx",
		},
		{name: "anything else stays with the vinyl data", entry: "notes.txt", want: "/vinyl"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, layout.DirFor(tc.entry))
		})
	}
}

// Every file has to land in the directory the instance will look for it in:
// Tarantool reads snapshots out of snapshot.dir, journals out of wal.dir and
// vinyl data out of vinyl.dir, and a file in the wrong one is a file the
// instance comes up without.
func TestApply_RoutesEveryKindIntoItsOwnDir(t *testing.T) {
	layout := splitLayout(t)

	result := applyMixed(t, layout)

	require.ElementsMatch(t, []string{snapName, xlogName, vylogName, runName, indexName},
		result.Files)

	assert.Equal(t, []string{snapName}, dirEntries(t, layout.Snapshot))
	assert.Equal(t, []string{xlogName}, dirEntries(t, layout.WAL))

	assert.FileExists(t, filepath.Join(layout.Vinyl, vylogName))
	assert.FileExists(t, filepath.Join(layout.Vinyl, filepath.FromSlash(runName)))
	assert.FileExists(t, filepath.Join(layout.Vinyl, filepath.FromSlash(indexName)))
}

// The stamp reaches every file wherever it landed: a .vylog left carrying
// another instance's UUID fails recovery before any data is replayed, and on a
// split layout it sits in a directory of its own, away from the snapshot.
func TestApply_PatchesHeadersInEveryDir(t *testing.T) {
	layout := splitLayout(t)

	result := applyMixed(t, layout)

	require.Equal(t, len(result.Files), result.Patched)

	for _, name := range result.Files {
		path := filepath.Join(layout.DirFor(name), filepath.FromSlash(name))
		assert.Equal(t, replicaUUID, readInstanceUUID(t, path), "instance uuid of %s", name)
	}
}

// The trim has to find the journal in wal.dir, and what starts past the point
// has to go from both directories: a snapshot past the point carries the
// instance beyond it exactly as an xlog does, and it lives elsewhere.
func TestApply_TrimsAndDropsAcrossSplitDirs(t *testing.T) {
	layout := splitLayout(t)

	result, err := Apply(ApplyOpts{
		Archives:  []string{chainPastPoint(t)},
		Layout:    layout,
		Point:     &Point{ReplicaID: 1, LSN: 5},
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	require.Equal(t, "00000000000000000003.xlog", result.TrimmedFile)
	require.ElementsMatch(t, []string{
		"00000000000000000006.snap",
		"00000000000000000006.xlog",
	}, result.DroppedFiles)

	assert.Equal(t, []string{snapName}, dirEntries(t, layout.Snapshot),
		"the snapshot past the point must go from the snapshot directory")
	assert.Equal(t, []string{xlogName, "00000000000000000003.xlog"},
		dirEntries(t, layout.WAL))

	assert.Equal(t, []rowKey{{1, 4}, {1, 5}},
		readRows(t, filepath.Join(layout.WAL, "00000000000000000003.xlog")),
		"the trimmed journal stays under its own name in the wal directory")
}

// A re-run starts from clean directories, all three of them: a stale file left
// in any one of them is replayed by the instance along with the real thing.
// What a restore does not own is left alone wherever it lives.
func TestApply_ClearsStaleFilesInEveryDir(t *testing.T) {
	layout := splitLayout(t)

	applyMixed(t, layout)

	stale := []string{
		filepath.Join(layout.Snapshot, "00000000000000000900.snap"),
		filepath.Join(layout.Snapshot, "00000000000000000900.sortdata"),
		filepath.Join(layout.WAL, "00000000000000000900.xlog"),
		filepath.Join(layout.WAL, "00000000000000000900.xlog.inprogress"),
		filepath.Join(layout.Vinyl, "512", "0", "00000000000000000900.run"),
	}
	foreign := []string{
		filepath.Join(layout.Snapshot, "config.yaml"),
		filepath.Join(layout.WAL, "tarantool.log"),
		filepath.Join(layout.Vinyl, "notes.txt"),
	}

	for _, path := range append(append([]string{}, stale...), foreign...) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	}

	applyMixed(t, layout)

	for _, path := range stale {
		assert.NoFileExists(t, path, "a stale restore artifact must be cleared")
	}

	for _, path := range foreign {
		assert.FileExists(t, path, "a file a restore does not own must survive")
	}
}

// The marker names one instance, and the snapshot directory belongs to one
// instance by construction, while the base directory a restore is called with
// can be shared by every instance of an application.
func TestApply_WritesTheMarkerBesideTheSnapshotDir(t *testing.T) {
	layout := splitLayout(t)
	base := t.TempDir()

	result, err := Apply(ApplyOpts{
		Archives:     []string{mixedArchive(t)},
		Layout:       layout,
		WorkDir:      base,
		InstanceName: "storage-001-a",
		PatchUUID:    replicaUUID,
	})
	require.NoError(t, err)

	require.Equal(t, layout.Snapshot+stateSuffix, result.StatePath)
	require.NoFileExists(t, base+stateSuffix,
		"the base directory is shared, so it is not where the marker goes")

	state, err := ReadState(layout.Snapshot)
	require.NoError(t, err)

	assert.Equal(t, StateSchemaVersion, state.SchemaVersion)
	assert.Equal(t, base, state.WorkDir)
	assert.Equal(t, layout.Snapshot, state.SnapshotDir)
	assert.Equal(t, layout.WAL, state.WALDir)
	assert.Equal(t, layout.Vinyl, state.VinylDir)
	assert.Equal(t, "storage-001-a", state.InstanceName)
}

// A rejected input is reported before any directory is touched, and that has
// to hold for every one of them: an orchestrator reads that exit code as
// "nothing happened" and retries on it.
func TestApply_RejectedInputLeavesEveryDirIntact(t *testing.T) {
	layout := splitLayout(t)

	good := applyMixed(t, layout)
	prepared := map[string][]string{}
	for _, dir := range []string{layout.Snapshot, layout.WAL, layout.Vinyl} {
		prepared[dir] = dirEntries(t, dir)
	}

	_, err := Apply(ApplyOpts{
		Archives:  []string{mixedArchive(t)},
		Checksums: []string{"0000000000000000000000000000000000000000000000000000000000000000"},
		Layout:    layout,
		PatchUUID: replicaUUID,
	})
	require.ErrorIs(t, err, ErrValidation)

	for dir, entries := range prepared {
		assert.ElementsMatch(t, entries, dirEntries(t, dir), "directory %s", dir)
	}

	require.FileExists(t, good.StatePath,
		"a rejected input must not take the marker of the previous attempt with it")
}

// A directory the backup put no file in -- an instance with no vinyl data is
// the ordinary case -- still has to be there afterwards. A restore creates the
// directories it is given and must not end having removed one of them. The
// sweep prunes what it empties, so a directory nested in another is taken away
// by that other one's pass and has to be put back.
func TestApply_LeavesAnEmptyNestedDirInPlace(t *testing.T) {
	root := t.TempDir()
	layout := Layout{
		Snapshot: filepath.Join(root, "data"),
		WAL:      filepath.Join(root, "data"),
		Vinyl:    filepath.Join(root, "data", "vinyl"),
	}

	full, inc := archiveChain(t)

	_, err := Apply(ApplyOpts{
		Archives:  []string{full, inc},
		Layout:    layout,
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	assert.DirExists(t, layout.Vinyl,
		"the vinyl directory must survive the sweep of the directory it sits in")
}

// An archive taken from an instance whose data directories nest names its
// entries against whichever directory holds them, so a vinyl run can arrive as
// "vinyl/512/0/<n>.run" and a snapshot as "snap/<n>.snap". Only the place a
// file occupies inside the directory of its own kind survives: anything else
// would land it one level too deep under the directory it is routed to, where
// the instance does not look for it.
func TestApply_NormalizesPrefixedEntryNames(t *testing.T) {
	layout := splitLayout(t)

	src := t.TempDir()
	snap := writeSnap(t, src, format.VClock{1: 0})
	xlog := writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2, 3))
	run := writeJournal(t, src, format.FiletypeRUN, format.VClock{1: 4}, nil, nil)

	prefixed := packRawArchive(t, filepath.Join(t.TempDir(), "prefixed.tar.zst"),
		rawEntry{name: "snap/" + snapName, path: snap},
		rawEntry{name: "wal/" + xlogName, path: xlog},
		rawEntry{name: "vinyl/" + runName, path: run},
	)

	result, err := Apply(ApplyOpts{Archives: []string{prefixed}, Layout: layout})
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{snapName, xlogName, runName}, result.Files)

	assert.Equal(t, []string{snapName}, dirEntries(t, layout.Snapshot))
	assert.Equal(t, []string{xlogName}, dirEntries(t, layout.WAL))
	assert.FileExists(t, filepath.Join(layout.Vinyl, filepath.FromSlash(runName)))
}

// A vinyl run has to say which space and index it belongs to, and a name whose
// last two directories are not ids says nothing of the kind. Refusing it
// before the directories are cleared is what keeps the promise the exit code
// makes: a rejected input leaves the previous attempt where it was.
func TestApply_RejectsAnUnreadableVinylEntryName(t *testing.T) {
	layout := splitLayout(t)

	prepared := applyMixed(t, layout)

	src := t.TempDir()
	run := writeJournal(t, src, format.FiletypeRUN, format.VClock{1: 4}, nil, nil)

	bad := packRawArchive(t, filepath.Join(t.TempDir(), "bad.tar.zst"),
		rawEntry{name: "a/b/c/d/00000000000000000004.run", path: run},
	)

	_, err := Apply(ApplyOpts{Archives: []string{bad}, Layout: layout})
	require.ErrorIs(t, err, ErrValidation)
	assert.ErrorContains(t, err, "<space_id>/<index_id>/<file>")

	assert.ElementsMatch(t, []string{snapName}, dirEntries(t, layout.Snapshot))
	assert.FileExists(t, prepared.StatePath,
		"a rejected entry name must not take the previous attempt with it")
}

// The restore package joins an entry name onto a directory of its own, so it
// answers for that name itself rather than trusting whoever handed it over.
func TestCanonicalEntryName(t *testing.T) {
	tests := []struct {
		name     string
		entry    string
		want     string
		reported string
	}{
		{name: "a snapshot", entry: snapName, want: snapName},
		{name: "a prefixed snapshot", entry: "snap/" + snapName, want: snapName},
		{name: "sort data", entry: sortDataName, want: sortDataName},
		{name: "a journal", entry: "wal/" + xlogName, want: xlogName},
		{name: "the vylog", entry: "vinyl/" + vylogName, want: vylogName},
		{name: "a vinyl run in its own tree", entry: runName, want: runName},
		{name: "a prefixed vinyl run", entry: "vinyl/" + runName, want: runName},
		{name: "a vinyl index", entry: "vinyl/" + indexName, want: indexName},
		{
			name:  "a flattened vinyl run keeps the only name it has",
			entry: "00000000000000000004.run",
			want:  "00000000000000000004.run",
		},
		{
			name:  "a vinyl run the backup caught half-written",
			entry: "vinyl/512/0/00000000000000000004.run.inprogress",
			want:  "512/0/00000000000000000004.run.inprogress",
		},
		{
			name:  "a journal the backup caught half-written",
			entry: "wal/" + xlogName + ".inprogress",
			want:  xlogName + ".inprogress",
		},
		{
			name:     "a vinyl run whose tree is not ids",
			entry:    "a/b/c/d/00000000000000000004.run",
			reported: "<space_id>/<index_id>/<file>",
		},
		{
			name:     "a vinyl run whose space is not an id",
			entry:    "wal/a/0/00000000000000000004.run",
			reported: "<space_id>/<index_id>/<file>",
		},
		{
			name:     "a vinyl run whose index is not an id",
			entry:    "512/x/00000000000000000004.run",
			reported: "<space_id>/<index_id>/<file>",
		},
		{
			name:     "a vinyl run with no index directory",
			entry:    "512/00000000000000000004.run",
			reported: "<space_id>/<index_id>/<file>",
		},
		{
			name:     "an escape out of the directory it is routed to",
			entry:    "../escape.snap",
			reported: "unsafe archive entry name",
		},
		{
			name:     "an absolute name",
			entry:    "/etc/escape.snap",
			reported: "unsafe archive entry name",
		},
		{
			name:     "a name that is not in canonical form",
			entry:    "./" + snapName,
			reported: "unsafe archive entry name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalEntryName(tc.entry)

			if tc.reported != "" {
				require.ErrorIs(t, err, ErrValidation)
				assert.ErrorContains(t, err, tc.reported)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A directory of the layout is one the caller named: a mount point, or a
// directory an operator created with a mode of their own. Cleanup empties it
// like any other and leaves it standing -- removing and recreating it would
// hand back a directory owned by whoever ran the restore, and on a mount point
// the removal fails with the files already gone.
func TestApply_KeepsTheModeOfAnEmptiedLayoutDir(t *testing.T) {
	root := t.TempDir()
	layout := Layout{
		Snapshot: filepath.Join(root, "data"),
		WAL:      filepath.Join(root, "data"),
		Vinyl:    filepath.Join(root, "data", "vinyl"),
	}

	const mode os.FileMode = 0o700

	require.NoError(t, os.MkdirAll(layout.Vinyl, 0o755))
	require.NoError(t, os.Chmod(layout.Vinyl, mode))

	stale := filepath.Join(layout.Vinyl, "00000000000000000900.run")
	require.NoError(t, os.WriteFile(stale, []byte("x"), 0o644))

	full, inc := archiveChain(t)

	_, err := Apply(ApplyOpts{Archives: []string{full, inc}, Layout: layout})
	require.NoError(t, err)

	assert.NoFileExists(t, stale, "a stale restore artifact must be cleared")

	info, err := os.Stat(layout.Vinyl)
	require.NoError(t, err)
	assert.Equal(t, mode, info.Mode().Perm(),
		"an emptied layout directory keeps the mode it was created with")
}

// A layout's directories are made absolute but not resolved through symbolic
// links, so a directory of the layout can be reached under a name none of them
// carries: with the snapshots in a symlink to the directory the vinyl data
// sits in, the sweep of the first arrives at the second as <alias>/vinyl. It
// is still the directory the caller named, mode, owner and all.
func TestApply_KeepsALayoutDirReachedThroughASymlink(t *testing.T) {
	root := t.TempDir()

	data := filepath.Join(root, "data")
	vinyl := filepath.Join(data, "vinyl")
	require.NoError(t, os.MkdirAll(vinyl, 0o755))

	const mode os.FileMode = 0o700
	require.NoError(t, os.Chmod(vinyl, mode))

	alias := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(data, alias))

	before, err := os.Stat(vinyl)
	require.NoError(t, err)

	layout := Layout{Snapshot: alias, WAL: alias, Vinyl: vinyl}

	full, inc := archiveChain(t)

	_, err = Apply(ApplyOpts{Archives: []string{full, inc}, Layout: layout})
	require.NoError(t, err)

	after, err := os.Stat(vinyl)
	require.NoError(t, err)

	assert.True(t, os.SameFile(before, after),
		"the vinyl directory must be the one the caller named, not one put back in its place")
	assert.Equal(t, mode, after.Mode().Perm(),
		"a layout directory keeps the mode it was created with")
}

// Two entries of one archive that land in one file are refused. Unpacking
// truncates what it writes, so the second would replace the first under a name
// that stands for whichever came last, and nothing in the result would say a
// file went missing.
func TestApply_RejectsTwoEntriesThatLandInOneFile(t *testing.T) {
	layout := splitLayout(t)

	prepared := applyMixed(t, layout)

	src := t.TempDir()
	snap := writeSnap(t, src, format.VClock{1: 0})

	collide := packRawArchive(t, filepath.Join(t.TempDir(), "collide.tar.zst"),
		rawEntry{name: "a/" + snapName, path: snap},
		rawEntry{name: "b/" + snapName, path: snap},
	)

	_, err := Apply(ApplyOpts{Archives: []string{collide}, Layout: layout})
	require.ErrorIs(t, err, ErrValidation)
	assert.ErrorContains(t, err, "both are restored as")

	assert.Equal(t, []string{snapName}, dirEntries(t, layout.Snapshot))
	assert.FileExists(t, prepared.StatePath,
		"a rejected archive must not take the previous attempt with it")
}

// Archives of one chain naming the same file is the ordinary case rather than
// a collision: they overlap on the boundary journal by construction, and the
// copy the later archive carries is the longer one.
func TestApply_AcceptsAChainWhoseArchivesShareAJournal(t *testing.T) {
	layout := splitLayout(t)

	full, inc := describedChain(t)

	result, err := Apply(ApplyOpts{Archives: []string{full, inc}, Layout: layout})
	require.NoError(t, err)

	require.Equal(t, []string{xlogName}, result.Files)
	assert.Equal(t, []rowKey{{1, 1}, {1, 2}, {1, 3}, {1, 4}},
		readRows(t, filepath.Join(layout.WAL, xlogName)),
		"the journal of the increment is the one that stays")
}

// A file Tarantool was still writing when the backup was taken carries
// .inprogress on top of its own extension, and the shape of its name is read
// off the extension underneath: an interrupted vinyl run belongs to a space
// and an index exactly as a finished one does.
func TestApply_KeepsTheTreeOfAnInProgressVinylRun(t *testing.T) {
	layout := splitLayout(t)

	src := t.TempDir()
	run := writeJournal(t, src, format.FiletypeRUN, format.VClock{1: 4}, nil, nil)

	const openRun = "512/0/00000000000000000004.run.inprogress"

	packed := packRawArchive(t, filepath.Join(t.TempDir(), "open.tar.zst"),
		rawEntry{name: "vinyl/" + openRun, path: run},
	)

	result, err := Apply(ApplyOpts{Archives: []string{packed}, Layout: layout})
	require.NoError(t, err)

	require.Equal(t, []string{openRun}, result.Files)
	assert.FileExists(t, filepath.Join(layout.Vinyl, filepath.FromSlash(openRun)))
}

// The restore package answers for the names it joins onto its own directories
// even when it never gets to look at them: the archive reader refuses a name
// that would write outside the directory it is unpacked into, and that is the
// archive naming a file the restore cannot place -- a rejected input, reported
// while the previous attempt is still intact.
func TestApply_RejectsAnEntryNameThatEscapesItsDir(t *testing.T) {
	layout := splitLayout(t)

	prepared := applyMixed(t, layout)

	src := t.TempDir()
	snap := writeSnap(t, src, format.VClock{1: 0})

	escaping := packRawArchive(t, filepath.Join(t.TempDir(), "escape.tar.zst"),
		rawEntry{name: "../escape.snap", path: snap},
	)

	_, err := Apply(ApplyOpts{Archives: []string{escaping}, Layout: layout})
	require.ErrorIs(t, err, ErrValidation)
	assert.ErrorContains(t, err, "unsafe archive entry name")

	assert.NoFileExists(t, filepath.Join(filepath.Dir(layout.Snapshot), "escape.snap"),
		"nothing may be written outside the directories of the layout")
	assert.Equal(t, []string{snapName}, dirEntries(t, layout.Snapshot))
	assert.FileExists(t, prepared.StatePath,
		"a rejected entry name must not take the previous attempt with it")
}

// The sort data of a snapshot describes that snapshot's indexes and is named
// after the same position, so one belonging to a snapshot past the point goes
// with it. Left behind, it describes a file that is no longer there.
func TestApply_DropsSortDataPastThePoint(t *testing.T) {
	layout := splitLayout(t)

	src := t.TempDir()
	files := []string{
		writeSnap(t, src, format.VClock{1: 0}),
		writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2, 3)),
		writeXlog(t, src, format.VClock{1: 3}, format.VClock{1: 0}, txsOf(1, 4, 5, 6)),
		writeSnap(t, src, format.VClock{1: 6}),
		writeXlog(t, src, format.VClock{1: 6}, format.VClock{1: 3}, txsOf(1, 7, 8, 9)),
		writeSortData(t, src, 0),
		writeSortData(t, src, 6),
	}

	result, err := Apply(ApplyOpts{
		Archives: []string{packArchive(t, filepath.Join(t.TempDir(), "wide.tar.zst"), files...)},
		Layout:   layout,
		Point:    &Point{ReplicaID: 1, LSN: 5},
	})
	require.NoError(t, err)

	assert.Contains(t, result.DroppedFiles, "00000000000000000006.sortdata")

	assert.ElementsMatch(t, []string{snapName, sortDataName},
		dirEntries(t, layout.Snapshot),
		"the sort data of the surviving snapshot stays")
}

// Two of the three keys pointing at one directory, or one directory sitting
// inside another, are both configurations Tarantool accepts -- and both make
// cleanup sweep a directory twice, or sweep away one it is about to need.
func TestApply_HandlesOverlappingDirs(t *testing.T) {
	tests := []struct {
		name   string
		layout func(root string) Layout
	}{
		{
			name: "snapshots and journals share a directory",
			layout: func(root string) Layout {
				return Layout{
					Snapshot: filepath.Join(root, "data"),
					WAL:      filepath.Join(root, "data"),
					Vinyl:    filepath.Join(root, "vinyl"),
				}
			},
		},
		{
			name: "the vinyl directory sits inside the snapshot one",
			layout: func(root string) Layout {
				return Layout{
					Snapshot: filepath.Join(root, "data"),
					WAL:      filepath.Join(root, "wal"),
					Vinyl:    filepath.Join(root, "data", "vinyl"),
				}
			},
		},
		{
			name: "all three are the same directory",
			layout: func(root string) Layout {
				return FlatLayout(filepath.Join(root, "data"))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			layout := tc.layout(t.TempDir())

			// Twice: the second run is the one that meets the directories the
			// first left, which is where a sweep that prunes what it empties
			// can take a directory another key still names.
			applyMixed(t, layout)
			result := applyMixed(t, layout)

			require.Len(t, result.Files, 5)

			for _, name := range result.Files {
				assert.FileExists(t,
					filepath.Join(layout.DirFor(name), filepath.FromSlash(name)))
			}
		})
	}
}
