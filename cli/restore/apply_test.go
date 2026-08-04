package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-xlog/dir"
	"github.com/tarantool/go-xlog/format"

	"github.com/tarantool/tt/cli/backup"
)

// applyOpts is the common call: the whole chain, patched, trimmed at lsn 5.
func applyOpts(t *testing.T, workDir string, point *Point) ApplyOpts {
	t.Helper()

	full, inc := archiveChain(t)

	return ApplyOpts{
		Archives:  []string{full, inc},
		Checksums: []string{checksumOf(t, full), checksumOf(t, inc)},
		WorkDir:   workDir,
		Point:     point,
		PatchUUID: replicaUUID,
	}
}

func TestApply_UnpacksChainInOrder(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	result, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	require.Equal(t, []string{
		"00000000000000000000.snap",
		"00000000000000000000.xlog",
		"00000000000000000003.xlog",
	}, result.Files)

	require.ElementsMatch(t, result.Files, dirEntries(t, workDir))
}

// The manifest fragment describes the backup, not the instance state, so it
// must not reach the work directory.
func TestApply_LeavesFragmentBehind(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	_, err := Apply(applyOpts(t, workDir, nil))
	require.NoError(t, err)

	require.NotContains(t, dirEntries(t, workDir), fragmentEntryName)
}

// Every snap and xlog takes the UUID the restored node has to own: a file left
// carrying another one makes Tarantool refuse the whole directory, and the
// .vylog is checked before recovery even starts.
func TestApply_PatchesEveryHeader(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	result, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	require.Equal(t, 3, result.Patched)

	for _, name := range result.Files {
		require.Equal(t, replicaUUID, readInstanceUUID(t, filepath.Join(workDir, name)),
			"instance uuid of %s", name)
	}
}

// Patching reaches every file in the archive that carries an instance UUID,
// not only the journals. Tarantool checks the .vylog header at startup and
// refuses to initialize storage on a mismatch ("invalid instance UUID"), and
// vinyl run/index files carry the same field.
func TestApply_PatchesVinylHeadersToo(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	src := t.TempDir()
	files := []string{
		writeSnap(t, src, format.VClock{1: 0}),
		writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2)),
		writeJournal(t, src, format.FiletypeVYLOG, format.VClock{}, nil, nil),
		writeJournal(t, src, format.FiletypeRUN, format.VClock{1: 4}, nil, nil),
		writeJournal(t, src, format.FiletypeINDEX, format.VClock{1: 5}, nil, nil),
	}

	result, err := Apply(ApplyOpts{
		Archives:  []string{packArchive(t, filepath.Join(t.TempDir(), "v.tar.zst"), files...)},
		WorkDir:   workDir,
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	require.Len(t, result.Files, len(files))

	for _, name := range result.Files {
		require.Equal(t, replicaUUID, readInstanceUUID(t, filepath.Join(workDir, name)),
			"instance uuid of %s", name)
	}
}

// The one file class the patch does not reach. An .inprogress journal -- the
// WAL Tarantool had open when the backup was taken -- is named by its
// extension, which is not one of the patched ones, so it lands still carrying
// the backed-up master's UUID while every sibling is re-stamped. Cleanup does
// own the extension, so a re-run stays consistent; what does not hold is the
// invariant that everything the work directory ends up holding is the node's.
func TestApply_InProgressEntryKeepsTheBackedUpMastersUUID(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	src := t.TempDir()
	open := writeXlog(t, src, format.VClock{1: 2}, format.VClock{1: 0}, txsOf(1, 3))
	require.NoError(t, os.Rename(open, open+".inprogress"))

	result, err := Apply(ApplyOpts{
		Archives: []string{packArchive(t, filepath.Join(t.TempDir(), "a.tar.zst"),
			writeSnap(t, src, format.VClock{1: 0}),
			writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2)),
			open+".inprogress")},
		WorkDir:   workDir,
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	require.Contains(t, result.Files, "00000000000000000002.xlog.inprogress")
	require.Equal(t, 2, result.Patched, "the open journal is not among the stamped headers")

	require.Equal(t, masterUUID,
		readInstanceUUID(t, filepath.Join(workDir, "00000000000000000002.xlog.inprogress")))
	require.Equal(t, replicaUUID,
		readInstanceUUID(t, filepath.Join(workDir, "00000000000000000000.xlog")),
		"its sibling of the same chain was re-stamped")
}

// Without --patch-uuid the headers keep the UUID they carry: the emergency
// case, where the target UUID cannot be established.
func TestApply_WithoutPatchUUIDKeepsHeaders(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	opts := applyOpts(t, workDir, nil)
	opts.PatchUUID = ""

	result, err := Apply(opts)
	require.NoError(t, err)

	require.Zero(t, result.Patched)
	require.Equal(t, masterUUID,
		readInstanceUUID(t, filepath.Join(workDir, "00000000000000000000.snap")))
}

// The chain replays whole except the tail of the final xlog.
func TestApply_TrimsFinalXlogAtPoint(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	result, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	require.Equal(t, "00000000000000000003.xlog", result.TrimmedFile)

	require.Equal(t, []rowKey{{1, 1}, {1, 2}, {1, 3}},
		readRows(t, filepath.Join(workDir, "00000000000000000000.xlog")),
		"earlier files must replay whole")
	require.Equal(t, []rowKey{{1, 4}, {1, 5}},
		readRows(t, filepath.Join(workDir, "00000000000000000003.xlog")))
}

// A point that falls exactly where the WAL rotated is one the chain can hit
// exactly -- the previous journal ends there -- and the restore overshoots it
// anyway: the lookup resolves "lsn 3" to the journal that *starts* at 3, and
// the trim keeps whole the first transaction it reads, so the instance comes up
// holding lsn 4, the transaction the operator asked to leave out. A point one
// row lower is cut exactly, which is how narrow the window is.
//
// Flip the boundary case once the point resolves to the journal holding the row.
func TestApply_PointOnAWalRotationBoundaryOvershootsIt(t *testing.T) {
	// archiveChain(t) rotates at lsn 3: 0.xlog holds 1..3, 3.xlog holds 4..6.
	tests := []struct {
		name    string
		point   Point
		trimmed string
		rows    []rowKey
	}{
		{
			name:    "inside a journal the point is exact",
			point:   Point{ReplicaID: 1, LSN: 2},
			trimmed: "00000000000000000000.xlog",
			rows:    []rowKey{{1, 1}, {1, 2}},
		},
		{
			name:    "on the rotation boundary one transaction too many survives",
			point:   Point{ReplicaID: 1, LSN: 3},
			trimmed: "00000000000000000003.xlog",
			rows:    []rowKey{{1, 4}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workDir := filepath.Join(t.TempDir(), "instance-001")

			result, err := Apply(applyOpts(t, workDir, &tc.point))
			require.NoError(t, err)

			require.Equal(t, tc.trimmed, result.TrimmedFile)
			require.Equal(t, tc.rows, readRows(t, filepath.Join(workDir, tc.trimmed)))
		})
	}
}

// Row keys say which rows survived, not what they carry: a copy that shuffled
// or clipped tuple bodies would satisfy every assertion above.
func TestApply_TrimKeepsRowBodiesIntact(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	_, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	require.Equal(t, [][]byte{mkBody(4), mkBody(5)},
		readRowBodies(t, filepath.Join(workDir, "00000000000000000003.xlog")),
		"the trimmed file's payloads")
	require.Equal(t, [][]byte{mkBody(1), mkBody(2), mkBody(3)},
		readRowBodies(t, filepath.Join(workDir, "00000000000000000000.xlog")),
		"an untouched file's payloads")
}

// The trimmed file stays indexable: Tarantool locates an xlog by the vclock
// signature in its name, so truncation must not disturb that.
func TestApply_TrimmedWorkDirStaysIndexable(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	_, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	indexed, err := dir.OpenDir(workDir, format.FiletypeXLOG)
	require.NoError(t, err)
	require.Len(t, indexed.Files(), 2)
}

// A backup covers a range and the point sits somewhere inside it, so the
// chain usually continues past the file being cut. Tarantool replays every
// journal it finds, so those files have to go — otherwise the instance runs
// straight through the point it was restored to and looks healthy doing it.
func TestApply_DropsFilesPastThePoint(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	result, err := Apply(ApplyOpts{
		Archives:  []string{chainPastPoint(t)},
		WorkDir:   workDir,
		Point:     &Point{ReplicaID: 1, LSN: 5},
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	require.Equal(t, "00000000000000000003.xlog", result.TrimmedFile)
	require.ElementsMatch(t, []string{
		"00000000000000000006.snap",
		"00000000000000000006.xlog",
	}, result.DroppedFiles, "a snapshot past the point is as fatal as an xlog")

	require.ElementsMatch(t, []string{
		"00000000000000000000.snap",
		"00000000000000000000.xlog",
		"00000000000000000003.xlog",
	}, dirEntries(t, workDir))

	// What is reported as landed is what a caller can still find there.
	require.ElementsMatch(t, result.Files, dirEntries(t, workDir))

	require.Equal(t, []rowKey{{1, 4}, {1, 5}},
		readRows(t, filepath.Join(workDir, "00000000000000000003.xlog")))
}

// A recovery point names a replica, and a master's journals carry the whole
// replicaset's positions, so the file holding lsn 3 of replica 2 is not the
// file holding lsn 3 of replica 1. Resolving the point on the wrong axis
// restores a shard that boots healthy at a position nobody asked for -- which
// is invisible to every fixture whose journals mention one replica.
func TestApply_MultiReplicaPointOnSecondaryAxis(t *testing.T) {
	tests := []struct {
		name    string
		point   Point
		trimmed string
		rows    []rowKey
	}{
		{
			name:    "on the replica the master's journals also carry",
			point:   Point{ReplicaID: 2, LSN: 3},
			trimmed: "00000000000000000006.xlog",
			rows:    []rowKey{{2, 2}, {1, 6}, {2, 3}},
		},
		{
			name:    "the same lsn on the master lands in another file",
			point:   Point{ReplicaID: 1, LSN: 3},
			trimmed: "00000000000000000000.xlog",
			rows:    []rowKey{{1, 1}, {1, 2}, {2, 1}, {1, 3}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workDir := filepath.Join(t.TempDir(), "instance-001")

			result, err := Apply(ApplyOpts{
				Archives:  []string{twoReplicaChain(t)},
				WorkDir:   workDir,
				Point:     &tc.point,
				PatchUUID: replicaUUID,
			})
			require.NoError(t, err)

			require.Equal(t, tc.trimmed, result.TrimmedFile)

			// The rows of the other replica written before the point are part
			// of the state it describes and stay; the ones after it go, whoever
			// wrote them.
			require.Equal(t, tc.rows, readRows(t, filepath.Join(workDir, tc.trimmed)))
		})
	}
}

// What is dropped past the point goes by the position each file name encodes --
// the signature of every replica's LSN together -- and not by the LSN the point
// names. On a multi-replica chain the two are different numbers, and taking the
// point's would delete the very file the point sits in.
func TestApply_DropsPastThePointBySignatureNotByReplicaLSN(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	// Signature 6 is where lsn 3 of replica 2 lives: 6 is above the point's 3
	// and above the file's own {2: 1}, so either of those as the threshold
	// takes the trimmed file down with the tail.
	result, err := Apply(ApplyOpts{
		Archives:  []string{twoReplicaChain(t)},
		WorkDir:   workDir,
		Point:     &Point{ReplicaID: 2, LSN: 3},
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	require.Equal(t, []string{"00000000000000000013.xlog"}, result.DroppedFiles)

	require.ElementsMatch(t, []string{
		"00000000000000000000.snap",
		"00000000000000000000.xlog",
		"00000000000000000006.xlog",
	}, dirEntries(t, workDir))
	require.ElementsMatch(t, result.Files, dirEntries(t, workDir))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}, {2, 1}, {1, 3}, {1, 4}, {1, 5}},
		readRows(t, filepath.Join(workDir, "00000000000000000000.xlog")),
		"a file wholly below the point replays whole on both axes")
}

// An entry name that is not already in canonical form (a "./" prefix here) is
// refused by the archive reader: Pack itself never produces one, so a reader
// seeing one knows the archive was not built by Pack.
func TestApply_RefusesANonCanonicalEntryName(t *testing.T) {
	src := t.TempDir()
	snap := writeSnap(t, src, format.VClock{1: 0})

	arch := packRawArchive(t, filepath.Join(t.TempDir(), "dot.tar.zst"),
		rawEntry{name: "./" + filepath.Base(snap), path: snap})

	workDir := filepath.Join(t.TempDir(), "instance-001")

	_, err := Apply(ApplyOpts{Archives: []string{arch}, WorkDir: workDir})
	require.ErrorContains(t, err, "./"+filepath.Base(snap), "the refused entry is named")

	require.NoFileExists(t, StatePath(workDir))
}

// A vinyl .run/.index entry name carries a <space_id>/<index_id>/ prefix, not
// a bare base name -- unlike snap/xlog, which sit flat in their data
// directory. Apply must accept it, land it in that subdirectory, and patch
// its header like any other journal, not refuse it as an unsafe entry name.
func TestApply_AcceptsAndPatchesNestedVinylEntry(t *testing.T) {
	src := t.TempDir()
	run := writeJournal(t, src, format.FiletypeRUN, format.VClock{1: 4}, nil, nil)

	arch := packRawArchive(t, filepath.Join(t.TempDir(), "vinyl.tar.zst"),
		rawEntry{name: "512/0/" + filepath.Base(run), path: run})

	workDir := filepath.Join(t.TempDir(), "instance-001")

	result, err := Apply(ApplyOpts{
		Archives:  []string{arch},
		WorkDir:   workDir,
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	name := "512/0/" + filepath.Base(run)
	require.Equal(t, []string{name}, result.Files)
	require.Equal(t, replicaUUID, readInstanceUUID(t, filepath.Join(workDir, name)))
}

// A point past the end of the chain trims nothing rather than emptying the
// last file.
func TestApply_PointAboveChainKeepsAll(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	_, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 99}))
	require.NoError(t, err)

	require.Equal(t, []rowKey{{1, 4}, {1, 5}, {1, 6}},
		readRows(t, filepath.Join(workDir, "00000000000000000003.xlog")))
}

// A point below every unpacked xlog has no file to cut: the point and the
// archives disagree, which is its own outcome, not a broken node.
func TestApply_PointBelowChainIsReported(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	src := t.TempDir()
	xlog := writeXlog(t, src, format.VClock{1: 10}, nil, txsOf(1, 11, 12))
	arch := packArchive(t, filepath.Join(t.TempDir(), "a.tar.zst"), xlog)

	_, err := Apply(ApplyOpts{
		Archives:  []string{arch},
		WorkDir:   workDir,
		Point:     &Point{ReplicaID: 1, LSN: 5},
		PatchUUID: replicaUUID,
	})
	require.ErrorIs(t, err, ErrNoTrimFile)
}

// Without a point the chain replays whole.
func TestApply_NoPointTrimsNothing(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	result, err := Apply(applyOpts(t, workDir, nil))
	require.NoError(t, err)

	require.Empty(t, result.TrimmedFile)
	require.Equal(t, []rowKey{{1, 4}, {1, 5}, {1, 6}},
		readRows(t, filepath.Join(workDir, "00000000000000000003.xlog")))
}

// A second run for the same point produces the same directory: the previous
// run's trimmed files are replaced, not trimmed again.
func TestApply_IsIdempotent(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	point := &Point{ReplicaID: 1, LSN: 5}

	first, err := Apply(applyOpts(t, workDir, point))
	require.NoError(t, err)
	firstRows := readRows(t, filepath.Join(workDir, "00000000000000000003.xlog"))

	second, err := Apply(applyOpts(t, workDir, point))
	require.NoError(t, err)

	require.Equal(t, first.Files, second.Files)
	require.Equal(t, first.TrimmedFile, second.TrimmedFile)
	require.Equal(t, firstRows, readRows(t, filepath.Join(workDir, "00000000000000000003.xlog")))
	require.ElementsMatch(t, second.Files, dirEntries(t, workDir))
}

// A re-run to an earlier point must not leave the deeper files of the
// previous attempt behind for Tarantool to replay.
func TestApply_ClearsFilesOfAPreviousAttempt(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	// Leftovers of an aborted run: a deeper xlog and every kind of
	// half-finished file this command or go-xlog can be interrupted on.
	writeXlog(t, workDir, format.VClock{1: 900}, nil, txsOf(1, 901))
	for _, name := range []string{
		"00000000000000000003.xlog.trimmed",
		"00000000000000000003.xlog.uuidpatch",
		"00000000000000000000.snap.uuidbak",
		"00000000000000000009.xlog.inprogress",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(workDir, name), []byte("x"), 0o644))
	}

	result, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	require.ElementsMatch(t, result.Files, dirEntries(t, workDir))
}

// A previous run's leftovers can be nested (vinyl's .run/.index files carry a
// <space_id>/<index_id>/ prefix): cleanup has to recurse into subdirectories
// to remove them, and prune any directory it leaves empty behind it, rather
// than only inspecting the top level of workDir.
func TestApply_ClearsNestedFilesOfAPreviousAttempt(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	vinylSubdir := filepath.Join(workDir, "512", "0")
	require.NoError(t, os.MkdirAll(vinylSubdir, 0o755))

	stale := filepath.Join(vinylSubdir, "00000000000000000900.run")
	require.NoError(t, os.WriteFile(stale, []byte("x"), 0o644))

	_, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	require.NoFileExists(t, stale)
	require.NoDirExists(t, vinylSubdir, "an emptied subdirectory must be pruned")
}

// Cleanup owns the files a restore produces, not the whole directory: an
// instance config or log living beside the data survives.
func TestApply_KeepsForeignFiles(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	keep := filepath.Join(workDir, "config.yaml")
	require.NoError(t, os.WriteFile(keep, []byte("groups: {}\n"), 0o644))

	_, err := Apply(applyOpts(t, workDir, nil))
	require.NoError(t, err)

	require.FileExists(t, keep)
}

// Cleanup's recursion must stop at a foreign file, not just a foreign top-level
// name: a subdirectory holding one is neither emptied nor pruned.
func TestApply_KeepsForeignNestedDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	foreignDir := filepath.Join(workDir, "512", "0")
	require.NoError(t, os.MkdirAll(foreignDir, 0o755))

	keep := filepath.Join(foreignDir, "notes.txt")
	require.NoError(t, os.WriteFile(keep, []byte("keep me"), 0o644))

	_, err := Apply(applyOpts(t, workDir, nil))
	require.NoError(t, err)

	require.FileExists(t, keep)
}

func TestApply_WritesStateMarker(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	point := &Point{ReplicaID: 1, LSN: 5}

	opts := applyOpts(t, workDir, point)
	opts.PointName = "7c9e6679-7425-40de-944b-e07fc1f90ae7"

	result, err := Apply(opts)
	require.NoError(t, err)

	require.Equal(t, workDir+".restore_state.json", result.StatePath)
	require.NotContains(t, dirEntries(t, workDir), "restore_state.json",
		"the marker belongs beside the work directory, not inside it")

	state, err := ReadState(workDir)
	require.NoError(t, err)

	require.Equal(t, StateSchemaVersion, state.SchemaVersion)
	require.Equal(t, workDir, state.WorkDir)
	require.Equal(t, "7c9e6679-7425-40de-944b-e07fc1f90ae7", state.PointName)
	require.Equal(t, point, state.TargetPoint)
	require.Equal(t, replicaUUID, state.InstanceUUID)
	require.Equal(t, []string{"full.tar.zst", "inc.tar.zst"}, state.Archives)
	require.False(t, state.AppliedAt.IsZero())
}

// A relative --work-dir names a directory just as well as an absolute one, and
// the marker belongs beside whichever it names: an orchestrator collects it as
// <workdir>.restore_state.json.
func TestStatePath_ResolvesRelativeWorkDirs(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "instance-001")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	t.Chdir(workDir)

	tests := []struct {
		name    string
		workDir string
		want    string
	}{
		{name: "the work directory itself", workDir: ".", want: workDir + stateSuffix},
		{name: "the parent directory", workDir: "..", want: root + stateSuffix},
		{name: "a trailing separator", workDir: "../instance-001/", want: workDir + stateSuffix},
		{name: "an absolute path", workDir: workDir, want: workDir + stateSuffix},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, StatePath(tc.workDir))
		})
	}
}

// The whole run has to agree on where the marker is: written beside the
// directory, reported at that path, and read back from it.
func TestApply_WritesTheMarkerBesideARelativeWorkDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	t.Chdir(workDir)

	result, err := Apply(applyOpts(t, ".", nil))
	require.NoError(t, err)

	require.Equal(t, workDir+stateSuffix, result.StatePath)
	require.FileExists(t, result.StatePath)

	for _, name := range dirEntries(t, ".") {
		require.NotContains(t, name, "restore_state.json",
			"the marker belongs beside the work directory, not inside it")
	}

	_, err = ReadState(".")
	require.NoError(t, err)
}

// The marker is now written beside the directory a relative --work-dir names,
// but the work_dir it records is still the spelling from the command line: a
// marker written for "--work-dir ." says ".", and once it is collected off the
// node it no longer says which directory it describes -- the one thing the
// field is for. Flip this when the recorded path is resolved like its own.
func TestApply_MarkerOfARelativeWorkDirRecordsItUnresolved(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	t.Chdir(workDir)

	_, err := Apply(applyOpts(t, ".", nil))
	require.NoError(t, err)

	state, err := ReadState(".")
	require.NoError(t, err)

	require.Equal(t, ".", state.WorkDir,
		"today the marker records the work directory unresolved")
}

// A run that dies partway must not leave the previous run's marker claiming
// the directory is ready.
func TestApply_FailedRunLeavesNoMarker(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	_, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)
	require.FileExists(t, StatePath(workDir))

	// Same directory, a point the new chain cannot cover.
	src := t.TempDir()
	xlog := writeXlog(t, src, format.VClock{1: 10}, nil, txsOf(1, 11))
	arch := packArchive(t, filepath.Join(t.TempDir(), "a.tar.zst"), xlog)

	_, err = Apply(ApplyOpts{
		Archives: []string{arch},
		WorkDir:  workDir,
		Point:    &Point{ReplicaID: 1, LSN: 5},
	})
	require.ErrorIs(t, err, ErrNoTrimFile)

	require.NoFileExists(t, StatePath(workDir))
}

// A rejected input is reported before anything is touched, so the previous
// attempt is still there to retry from.
func TestApply_ChecksumMismatchLeavesWorkDirIntact(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	good, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	opts := applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5})
	opts.Checksums[1] = "0000000000000000000000000000000000000000000000000000000000000000"

	_, err = Apply(opts)
	require.ErrorIs(t, err, ErrValidation)

	require.ElementsMatch(t, good.Files, dirEntries(t, workDir))
	require.FileExists(t, StatePath(workDir))
}

// Checksums are hex; case must not decide whether an archive is accepted.
func TestApply_ChecksumComparisonIgnoresCase(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	opts := applyOpts(t, workDir, nil)
	for i, sum := range opts.Checksums {
		opts.Checksums[i] = strings.ToUpper(sum)
	}

	_, err := Apply(opts)
	require.NoError(t, err)
}

func TestApply_RejectsBadInput(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	full, inc := archiveChain(t)

	tests := []struct {
		name string
		opts ApplyOpts
	}{
		{
			name: "no archives",
			opts: ApplyOpts{WorkDir: workDir},
		},
		{
			name: "no work dir",
			opts: ApplyOpts{Archives: []string{full}},
		},
		{
			name: "checksum count mismatch",
			opts: ApplyOpts{
				Archives:  []string{full, inc},
				Checksums: []string{checksumOf(t, full)},
				WorkDir:   workDir,
			},
		},
		{
			name: "archive missing",
			opts: ApplyOpts{
				Archives: []string{filepath.Join(t.TempDir(), "absent.tar.zst")},
				WorkDir:  workDir,
			},
		},
		{
			name: "archive is a directory",
			opts: ApplyOpts{Archives: []string{t.TempDir()}, WorkDir: workDir},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Apply(tc.opts)
			require.ErrorIs(t, err, ErrValidation)
		})
	}
}

// An unparseable --patch-uuid is an input error, and inputs are rejected
// before the work directory is touched. Parsing it only where the headers are
// stamped would take the previous attempt down with a plain failure.
func TestApply_RejectsBadPatchUUIDBeforeTouchingWorkDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	good, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	opts := applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5})
	opts.PatchUUID = "not-a-uuid"

	_, err = Apply(opts)
	require.ErrorIs(t, err, ErrValidation)

	require.ElementsMatch(t, good.Files, dirEntries(t, workDir),
		"the previous attempt must survive a rejected input")
	require.FileExists(t, StatePath(workDir))
}

// The boundary xlog ships in both the full backup and the increment that
// continues it, so the same name lands twice. The archive applied later has
// the longer file and must win, and going the other way must not leave the
// longer file's tail behind.
func TestApply_LaterArchiveWinsOnOverlap(t *testing.T) {
	shortDir, longDir := t.TempDir(), t.TempDir()
	short := packArchive(t, filepath.Join(t.TempDir(), "short.tar.zst"),
		writeXlog(t, shortDir, format.VClock{1: 0}, nil, txsOf(1, 1, 2)))
	long := packArchive(t, filepath.Join(t.TempDir(), "long.tar.zst"),
		writeXlog(t, longDir, format.VClock{1: 0}, nil, txsOf(1, 1, 2, 3, 4)))

	tests := []struct {
		name     string
		archives []string
		want     []rowKey
	}{
		{
			name:     "increment extends the file",
			archives: []string{short, long},
			want:     []rowKey{{1, 1}, {1, 2}, {1, 3}, {1, 4}},
		},
		{
			name:     "shorter copy lands last",
			archives: []string{long, short},
			want:     []rowKey{{1, 1}, {1, 2}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workDir := filepath.Join(t.TempDir(), "instance-001")

			_, err := Apply(ApplyOpts{Archives: tc.archives, WorkDir: workDir})
			require.NoError(t, err)

			require.Equal(t, tc.want,
				readRows(t, filepath.Join(workDir, "00000000000000000000.xlog")))
		})
	}
}

// A chain applied in an order it was not taken in restores an instance that
// boots healthy and silently stops short: the archives share the boundary
// journal, the last write wins, and the increment's longer copy is overwritten
// by the full backup's. The marker such a run leaves is indistinguishable from
// the one every other node wrote, which is the divergence it exists to rule
// out -- so the chain has to be refused instead.
func TestApply_RefusesAChainThatIsNotAChain(t *testing.T) {
	full, inc := archiveChain(t)
	describedFull, describedInc := describedChain(t)

	// Begins past the end of the full backup: the increment continuing it is
	// missing from the chain.
	gap := describedArchive(t, "gap.tar.zst", 9, []int64{10, 11, 12},
		fragmentOf(backup.BackupTypeIncremental, 9, 12))

	foreign := fragmentOf(backup.BackupTypeIncremental, 2, 8)
	foreign.ReplicasetUUID = "44444444-4444-4444-4444-444444444444"
	foreign.InstanceUUID = "99999999-9999-9999-9999-999999999999"
	other := describedArchive(t, "other.tar.zst", 2, []int64{3, 4}, foreign)

	tests := []struct {
		name     string
		archives []string
		reported string
	}{
		{
			name:     "reversed, told apart by the journal names",
			archives: []string{inc, full},
			reported: "holds no journal past 00000000000000000000",
		},
		{
			name:     "reversed, told apart by the fragments alone",
			archives: []string{describedInc, describedFull},
			reported: "ends at 2, before",
		},
		{
			name:     "an increment beginning past the end of the previous archive",
			archives: []string{describedFull, gap},
			reported: "an increment of the chain is missing",
		},
		{
			name:     "archives of two different instances",
			archives: []string{describedFull, other},
			reported: "a chain is one instance's",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workDir := filepath.Join(t.TempDir(), "instance-001")

			_, err := Apply(ApplyOpts{
				Archives:  tc.archives,
				WorkDir:   workDir,
				PatchUUID: replicaUUID,
			})
			require.ErrorIs(t, err, ErrValidation)
			require.ErrorContains(t, err, tc.reported)

			require.NoFileExists(t, StatePath(workDir),
				"a refused chain must not leave a marker claiming the directory is ready")
		})
	}
}

// A refused chain is a rejected input like any other, so it must leave the
// previous attempt exactly as it was: an orchestrator reads that exit code as
// "nothing happened" and retries on it.
func TestApply_RefusedChainLeavesWorkDirIntact(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	good, err := Apply(applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5}))
	require.NoError(t, err)

	trimmed := readRows(t, filepath.Join(workDir, "00000000000000000003.xlog"))

	opts := applyOpts(t, workDir, &Point{ReplicaID: 1, LSN: 5})
	opts.Archives[0], opts.Archives[1] = opts.Archives[1], opts.Archives[0]
	opts.Checksums[0], opts.Checksums[1] = opts.Checksums[1], opts.Checksums[0]

	_, err = Apply(opts)
	require.ErrorIs(t, err, ErrValidation)

	require.ElementsMatch(t, good.Files, dirEntries(t, workDir),
		"the previous attempt must survive a refused chain")
	require.FileExists(t, StatePath(workDir),
		"a rejected input must not take the marker of the previous attempt with it")
	require.Equal(t, trimmed, readRows(t, filepath.Join(workDir, "00000000000000000003.xlog")),
		"the trimmed journal must still be the one the previous attempt left")
}

// The same archives in the order they were taken apply as they always did: the
// increment's copy of the boundary journal is the one that stays, and a name
// that lands twice is one file, reported once.
func TestApply_AppliesADescribedChainInOrder(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	full, inc := describedChain(t)

	result, err := Apply(ApplyOpts{Archives: []string{full, inc}, WorkDir: workDir})
	require.NoError(t, err)

	require.Equal(t, []rowKey{{1, 1}, {1, 2}, {1, 3}, {1, 4}},
		readRows(t, filepath.Join(workDir, "00000000000000000000.xlog")))

	require.Equal(t, []string{"00000000000000000000.xlog"}, result.Files)
	require.ElementsMatch(t, result.Files, dirEntries(t, workDir))
}

// Cleanup goes by extension, and a dotfile is not one: ".snap" is somebody's
// own file, not a snapshot this command may delete.
func TestApply_KeepsDotfileNamedLikeAnExtension(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")
	require.NoError(t, os.MkdirAll(workDir, 0o755))

	for _, name := range []string{".snap", ".xlog", ".vylog"} {
		require.NoError(t, os.WriteFile(
			filepath.Join(workDir, name), []byte("not a journal"), 0o644))
	}

	_, err := Apply(applyOpts(t, workDir, nil))
	require.NoError(t, err)

	for _, name := range []string{".snap", ".xlog", ".vylog"} {
		require.FileExists(t, filepath.Join(workDir, name), "%s must survive cleanup", name)
	}
}

// A UUID can be on disk in a spelling narrower than the canonical one — the
// 32-char hyphenless form reads back identically but is four bytes shorter, so
// overwriting it in place would shift the rest of the header. The patch then
// goes through a copy, and must leave neither the old UUID nor a temp file.
func TestApply_PatchesHeaderOfNonCanonicalWidth(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	src := t.TempDir()
	snap := writeSnap(t, src, format.VClock{1: 0})
	xlog := writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2))
	compactInstanceUUID(t, snap)

	result, err := Apply(ApplyOpts{
		Archives:  []string{packArchive(t, filepath.Join(t.TempDir(), "a.tar.zst"), snap, xlog)},
		WorkDir:   workDir,
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	require.Equal(t, 2, result.Patched)
	require.Equal(t, replicaUUID,
		readInstanceUUID(t, filepath.Join(workDir, "00000000000000000000.snap")))

	require.ElementsMatch(t, result.Files, dirEntries(t, workDir),
		"the copy used to rewrite the header must not be left behind")
}

// A journal that cannot be read is a failure, not "the point is not here":
// the two carry different exit codes, so they must not collapse into one.
func TestApply_CorruptXlogIsNotAMissingPoint(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	src := t.TempDir()
	arch := packArchive(t, filepath.Join(t.TempDir(), "a.tar.zst"),
		writeXlog(t, src, format.VClock{1: 0}, nil, txsOf(1, 1, 2)),
		writeGarbageXlog(t, src, 9))

	_, err := Apply(ApplyOpts{
		Archives: []string{arch},
		WorkDir:  workDir,
		Point:    &Point{ReplicaID: 1, LSN: 1},
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoTrimFile)
	require.NotErrorIs(t, err, ErrValidation)
}

// The same file fails loudly while the headers are being stamped, rather than
// being skipped as unpatchable.
func TestApply_CorruptJournalFailsPatching(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	src := t.TempDir()
	arch := packArchive(t, filepath.Join(t.TempDir(), "a.tar.zst"),
		writeGarbageXlog(t, src, 9))

	_, err := Apply(ApplyOpts{
		Archives:  []string{arch},
		WorkDir:   workDir,
		PatchUUID: replicaUUID,
	})
	require.ErrorContains(t, err, "failed to patch instance uuid")
}

// Only files carrying an Instance line are patched; anything else in the
// archive lands untouched instead of failing the run.
func TestApply_LeavesFilesWithoutAHeaderAlone(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "instance-001")

	src := t.TempDir()
	notes := filepath.Join(src, "notes.txt")
	require.NoError(t, os.WriteFile(notes, []byte("operator notes"), 0o644))

	result, err := Apply(ApplyOpts{
		Archives: []string{packArchive(t, filepath.Join(t.TempDir(), "a.tar.zst"),
			writeSnap(t, src, format.VClock{1: 0}), notes)},
		WorkDir:   workDir,
		PatchUUID: replicaUUID,
	})
	require.NoError(t, err)

	require.Equal(t, 1, result.Patched, "only the snapshot carries a header")

	body, err := os.ReadFile(filepath.Join(workDir, "notes.txt"))
	require.NoError(t, err)
	require.Equal(t, "operator notes", string(body))
}

// A work directory that cannot be prepared is reported, not worked around.
func TestApply_ReportsAWorkDirItCannotPrepare(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	parent := t.TempDir()
	require.NoError(t, os.Chmod(parent, 0o500))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	_, err := Apply(applyOpts(t, filepath.Join(parent, "instance-001"), nil))
	require.ErrorContains(t, err, "failed to create work directory")
}
