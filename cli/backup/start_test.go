package backup

import (
	"archive/tar"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool"

	"github.com/tarantool/tt/cli/backup/archive"
	"github.com/tarantool/tt/cli/connector"
)

// mockEvaler is a connector.Connector stub that returns queued results in call
// order and records every Eval expression and args. nil queue entries are
// returned as (nil, nil); a non-nil err is returned with the queued result.
type mockEvaler struct {
	exprs    []string
	argsList [][]any
	queue    [][]any
	err      error
	errOn    int
	calls    int
}

func (m *mockEvaler) Eval(expr string, args []any,
	opts connector.RequestOpts,
) ([]any, error) {
	m.exprs = append(m.exprs, expr)
	m.argsList = append(m.argsList, args)
	m.calls++
	if m.errOn == m.calls && m.err != nil {
		return nil, m.err
	}
	if m.queue != nil && m.calls-1 < len(m.queue) {
		return m.queue[m.calls-1], nil
	}
	return nil, nil
}

func (m *mockEvaler) Close() error { return nil }

// Golden fixtures for the start command: fixed WAL payloads and an expected
// instance_backup.json. checksum_sha256 is dynamic (the sha256 of the
// produced archive), so it is masked out in tests.
var (
	//go:embed testdata/start/snap.bin
	goldenSnap []byte
	//go:embed testdata/start/xlog.bin
	goldenXlog []byte
	//go:embed testdata/start/golden_instance_backup.json
	goldenFragment []byte
)

// fragmentPathFor returns the manifest fragment path that corresponds to the
// given archive path (same base name, .json instead of .tar.zst).
func fragmentPathFor(archivePath string) string {
	return strings.TrimSuffix(archivePath, ".tar.zst") + ".json"
}

// writeWAL places a file named name with the given content into dir and returns
// its path; used to back resolveFiles in tests.
func writeWAL(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0o644))
}

// readArchiveOrder returns entry names in archive order.
func readArchiveOrder(t *testing.T, path string) []string {
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
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		names = append(names, h.Name)
	}
	return names
}

// readArchiveEntries returns name->content of a .tar.zst archive.
func readArchiveEntries(t *testing.T, path string) map[string][]byte {
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
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		got[h.Name] = body
	}
	return got
}

// infoMap builds the raw box.backup.info() response shape (map[any]any, as
// net.box decodes msgpack) for a backup with the given files, vclocks and
// recovery points. vbegin is mapped to prev_vclock (incremental begin);
// vend is mapped to vclock (end). rps == nil omits the recovery_points key;
// an empty non-nil slice yields recovery_points = [].
func infoMap(files []string, vbegin, vend Vclock, rps *[]map[any]any) map[any]any {
	backupType := BackupTypeFull
	if len(vbegin) > 0 {
		backupType = BackupTypeIncremental
	}
	m := map[any]any{
		"files":  toAnySlice(files),
		"type":   backupType,
		"vclock": toVclockMap(vend),
	}
	if len(vbegin) > 0 {
		m["prev_vclock"] = toVclockMap(vbegin)
	}
	if rps != nil {
		list := make([]any, len(*rps))
		for i, rp := range *rps {
			list[i] = rp
		}
		m["recovery_points"] = list
	}
	return m
}

func toAnySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

func toVclockMap(vc Vclock) map[any]any {
	m := map[any]any{}
	for k, v := range vc {
		m[uint64(k)] = uint64(v)
	}
	return m
}

// instanceMap builds the raw instance info response (map[any]any). The
// replicaset uuid is always testReplicasetUUID across these tests.
func instanceMap(
	instanceName,
	walDir,
	memtxDir string,
) map[any]any {
	return map[any]any{
		"replicaset_uuid": testReplicasetUUID,
		"instance_uuid":   testInstanceUUID,
		"instance_name":   instanceName,
		"hostname":        "node-1.example.com",
		"wal_dir":         walDir,
		"memtx_dir":       memtxDir,
		"vinyl_dir":       "",
	}
}

const (
	testReplicasetUUID = "11111111-1111-1111-1111-111111111111"
	testInstanceUUID   = "aaaaaaaa-0000-0000-0000-000000000001"
)

// walFiles is the set of WAL files used across start tests.
var walFiles = []string{
	"00000000000000001500.snap",
	"00000000000000001500.xlog",
}

// writeWalFiles writes walFiles into dir.
func writeWalFiles(t *testing.T, dir string) {
	t.Helper()
	writeWAL(t, dir, "00000000000000001500.snap", []byte("snap"))
	writeWAL(t, dir, "00000000000000001500.xlog", []byte("xlog"))
}

// startQueue builds the mockEvaler queue for a successful StartBackup: info
// (none) → start → info (open) → instance info.
func startQueue(infoOpen, inst map[any]any) [][]any {
	return [][]any{
		nil,
		nil,
		{infoOpen},
		{inst},
	}
}

func TestStartBackup_buildsArchiveAndLeavesBackupOpen(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWalFiles(t, walDir)

	info := infoMap(walFiles, nil, Vclock{1: 1502, 2: 230}, &[]map[any]any{
		{
			"label":      "rp-1",
			"replica_id": uint64(1),
			"lsn":        uint64(1501),
			"timestamp":  float64(1741780500),
		},
	})
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	archivePath, err := Start(m, BackupStartOpts{BackupID: "20260312T120000Z"})
	require.NoError(t, err)

	// Archive exists at the expected path.
	expectedDir := filepath.Join(os.TempDir(), localBackupRootDir, "20260312T120000Z")
	require.Equal(t,
		filepath.Join(expectedDir, "20260312T120000Z-"+testReplicasetUUID+".tar.zst"),
		archivePath)
	_, statErr := os.Stat(archivePath)
	require.NoError(t, statErr)

	// Archive contains only snap/xlog, ordered by LSN. The fragment is kept
	// next to the archive, not inside it.
	require.Equal(t, []string{
		"00000000000000001500.snap",
		"00000000000000001500.xlog",
	}, readArchiveOrder(t, archivePath))
	_, fragStatErr := os.Stat(fragmentPathFor(archivePath))
	require.NoError(t, fragStatErr)

	// box.backup was started and never stopped (left open for finalize).
	require.True(t, slices.Contains(m.exprs, "box.backup.start(...)"), "start must be called")
	require.False(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must not be called")
}

func TestStartBackup_fragmentFields(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWAL(t, walDir, "00000000000000001500.snap", goldenSnap)
	writeWAL(t, walDir, "00000000000000001500.xlog", goldenXlog)

	info := infoMap(walFiles, nil, Vclock{1: 1502, 2: 230}, &[]map[any]any{
		{
			"label":      "rp-1",
			"replica_id": uint64(1),
			"lsn":        uint64(1501),
			"timestamp":  float64(1741780500),
		},
	})

	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	archivePath, err := Start(m, BackupStartOpts{BackupID: "bid"})
	require.NoError(t, err)

	// Archive carries the golden WAL payloads byte-for-byte.
	entries := readArchiveEntries(t, archivePath)
	require.Equal(t, goldenSnap, entries["00000000000000001500.snap"])
	require.Equal(t, goldenXlog, entries["00000000000000001500.xlog"])

	// Fragment matches the golden fixture (hostname and checksum are dynamic).
	fragmentData, err := os.ReadFile(fragmentPathFor(archivePath))
	require.NoError(t, err)
	var frag Fragment
	require.NoError(t, json.Unmarshal(fragmentData, &frag))
	var golden Fragment
	require.NoError(t, json.Unmarshal(goldenFragment, &golden))
	require.Equal(t, golden.ReplicasetUUID, frag.ReplicasetUUID)
	require.Equal(t, golden.InstanceUUID, frag.InstanceUUID)
	require.Equal(t, golden.InstanceName, frag.InstanceName)
	require.Equal(t, "node-1.example.com", frag.Hostname)
	require.Equal(t, golden.Type, frag.Type)
	require.Equal(t, golden.VclockBegin, frag.VclockBegin)
	require.Equal(t, golden.VclockEnd, frag.VclockEnd)
	require.Equal(t, golden.Files, frag.Files)
	require.Equal(t, golden.RecoveryPoints, frag.RecoveryPoints)

	// checksum_sha256 is the sha256 of the produced archive.
	wantSum, err := archive.Checksum(archivePath)
	require.NoError(t, err)
	require.Equal(t, wantSum, frag.ChecksumSHA256)
}

// TestStartBackup_failLoudOnAlreadyOpen checks that an already-open box.backup
// fails.
func TestStartBackup_failLoudOnAlreadyOpen(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	info := infoMap(walFiles, Vclock{1: 1200}, Vclock{1: 1502}, nil)
	m := &mockEvaler{queue: [][]any{{info}}}

	_, err := Start(m, BackupStartOpts{BackupID: "bid"})
	require.ErrorIs(t, err, ErrAlreadyInProgress)
	require.ErrorContains(t, err, "type=incremental")
	require.ErrorContains(t, err, "vclock_begin=map[1:1200]")

	require.False(t, slices.Contains(m.exprs, "box.backup.start(...)"))
	require.False(t, slices.Contains(m.exprs, "box.backup.stop()"))
}

// TestStartBackup_fromVclockPropagated checks --from-vclock reaches
// box.backup.start.
func TestStartBackup_fromVclockPropagated(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWalFiles(t, walDir)

	info := infoMap(walFiles, Vclock{1: 42, 2: 7}, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	archivePath, err := Start(m, BackupStartOpts{
		BackupID:   "bid",
		FromVclock: Vclock{1: 42, 2: 7},
	})
	require.NoError(t, err)

	// box.backup.start(...) is the 2nd call → argsList[1].
	require.Len(t, m.argsList, 4)
	startArgs := m.argsList[1]
	require.Len(t, startArgs, 1)
	got := startArgs[0].(map[string]any)
	require.Equal(t, map[uint32]uint64{1: 42, 2: 7}, got["from_vclock"])

	fragmentData, err := os.ReadFile(fragmentPathFor(archivePath))
	require.NoError(t, err)
	var fragment Fragment
	require.NoError(t, json.Unmarshal(fragmentData, &fragment))
	require.Equal(t, BackupTypeIncremental, fragment.Type)
	require.Equal(t, Vclock{1: 42, 2: 7}, fragment.VclockBegin)
	require.Equal(t, Vclock{1: 1502}, fragment.VclockEnd)
}

// TestStartBackup_fullBackupOmitsFromVclock checks a full backup (FromVclock
// nil) does not send from_vclock to box.backup.start.
func TestStartBackup_fullBackupOmitsFromVclock(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWalFiles(t, walDir)

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	_, err := Start(m, BackupStartOpts{BackupID: "bid"}) // FromVclock nil → full
	require.NoError(t, err)

	startArgs := m.argsList[1]
	got := startArgs[0].(map[string]any)
	_, hasFromVclock := got["from_vclock"]
	require.False(t, hasFromVclock, "full backup must not send from_vclock")
}

// TestStartBackup_ttlPropagated checks opts.TTL reaches box.backup.start as
// seconds.
func TestStartBackup_ttlPropagated(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWalFiles(t, walDir)

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	_, err := Start(m, BackupStartOpts{BackupID: "bid", TTL: 30 * time.Minute})
	require.NoError(t, err)

	startArgs := m.argsList[1]
	got := startArgs[0].(map[string]any)
	require.Equal(t, float64(1800), got["ttl"])
}

// TestStartBackup_recoveryPointsStates checks the "absent / empty / populated"
// distinction is preserved in the packed instance_backup.json.
func TestStartBackup_recoveryPointsStates(t *testing.T) {
	tests := []struct {
		name      string
		rps       *[]map[any]any
		wantField bool
		wantLen   int
	}{
		{name: "absent", rps: nil, wantField: false},
		{name: "empty", rps: &[]map[any]any{}, wantField: true, wantLen: 0},
		{name: "populated", rps: &[]map[any]any{
			{"label": "rp-1", "replica_id": uint64(1), "lsn": uint64(1), "timestamp": float64(1)},
		}, wantField: true, wantLen: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			walDir := t.TempDir()
			writeWalFiles(t, walDir)

			info := infoMap(walFiles, nil, Vclock{1: 1502}, tc.rps)
			inst := instanceMap("router-001", walDir, "")
			m := &mockEvaler{queue: startQueue(info, inst)}

			archivePath, err := Start(m, BackupStartOpts{BackupID: "bid"})
			require.NoError(t, err)

			fragmentData, err := os.ReadFile(fragmentPathFor(archivePath))
			require.NoError(t, err)
			// Inspect raw JSON to tell "absent" from "empty list".
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(fragmentData, &raw))
			_, present := raw["recovery_points"]
			require.Equal(t, tc.wantField, present, "recovery_points presence mismatch")
			if tc.wantField {
				var got []*RecoveryPoint
				require.NoError(t, json.Unmarshal(raw["recovery_points"], &got))
				require.Len(t, got, tc.wantLen)
			}
		})
	}
}

// TestStartBackup_fileNotFound checks resolveFiles surfaces a clear error when
// a WAL file listed by box.backup.info() is missing on disk.
func TestStartBackup_fileNotFound(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	_, err := Start(m, BackupStartOpts{BackupID: "bid"})
	require.ErrorContains(t, err, "not found in data directories")
}

// TestStartBackup_instNameFallback checks the <APP:INSTANCE> instance name is
// used when box.info.name is empty.
func TestStartBackup_instNameFallback(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWalFiles(t, walDir)

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("", walDir, "")
	delete(inst, "instance_name")
	m := &mockEvaler{queue: startQueue(info, inst)}

	archivePath, err := Start(m, BackupStartOpts{
		BackupID: "bid",
		InstName: "router-001",
	})
	require.NoError(t, err)

	fragmentData, err := os.ReadFile(fragmentPathFor(archivePath))
	require.NoError(t, err)
	var frag Fragment
	require.NoError(t, json.Unmarshal(fragmentData, &frag))
	require.Equal(t, "router-001", frag.InstanceName)
}

// TestStartBackup_infoError checks a box.backup.info() error propagates.
func TestStartBackup_infoError(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	m := &mockEvaler{err: errors.New("dial: connection refused"), errOn: 1}

	_, err := Start(m, BackupStartOpts{BackupID: "bid"})
	require.ErrorContains(t, err, "connection refused")
}

// TestStartBackup_startError checks a box.backup.start() error propagates and
// is not swallowed.
func TestStartBackup_startError(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	m := &mockEvaler{err: errors.New("boom"), errOn: 2}
	m.queue = [][]any{nil}

	_, err := Start(m, BackupStartOpts{BackupID: "bid"})
	require.ErrorContains(t, err, "boom")
}

// The pre-check cannot catch a run that lost the race after it: both starts
// read an empty box.backup.info() and only the instance rejects the second.
// That loser is exactly as stuck as one the pre-check caught, so it has to
// carry the same error -- runBackupStart routes ErrAlreadyInProgress to exit
// 2, and an orchestrator keys its stuck-backup branch on that code alone.
func TestStartBackup_mapsInstanceSideAlreadyInProgress(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	m := &mockEvaler{errOn: 2, err: errors.New(
		"Backup is already in progress (ClientError, code 0x81), see eval line 1")}
	m.queue = [][]any{nil}

	_, err := Start(m, BackupStartOpts{BackupID: "bid"})

	require.ErrorIs(t, err, ErrAlreadyInProgress)
}

// TestStartBackup_mapsAlreadyInProgressByErrorCode pins the signal the mapping
// is meant to key on: ER_BACKUP_IN_PROGRESS survives message rewording across
// Tarantool versions, so a refusal must be recognised by its code alone.
func TestStartBackup_mapsAlreadyInProgressByErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "response code",
			err: tarantool.Error{
				Code: uint32(iproto.ER_BACKUP_IN_PROGRESS),
				Msg:  "reworded in a later version",
			},
		},
		{
			name: "box error code",
			err: tarantool.Error{
				Code: uint32(iproto.ER_PROC_LUA),
				Msg:  "reworded in a later version",
				ExtendedInfo: &tarantool.BoxError{
					Type: "ClientError",
					Code: uint64(iproto.ER_BACKUP_IN_PROGRESS),
					Msg:  "reworded in a later version",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			m := &mockEvaler{errOn: 2, err: tc.err, queue: [][]any{nil}}

			_, err := Start(m, BackupStartOpts{BackupID: "bid"})

			require.ErrorIs(t, err, ErrAlreadyInProgress)
		})
	}
}

// TestStartBackup_cleansUpOnFragmentWriteFailure checks that if writing the
// fragment fails after the archive has been packed, both the archive and the
// half-written fragment are removed.
func TestStartBackup_cleansUpOnFragmentWriteFailure(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWalFiles(t, walDir)

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	// Pre-create the fragment path as a directory so writeFragment's WriteFile
	// fails with "is a directory".
	archiveDir := filepath.Join(os.TempDir(), localBackupRootDir, "cleanup-test")
	require.NoError(t, os.MkdirAll(archiveDir, 0o755))

	expectedArchivePath := filepath.Join(archiveDir,
		"cleanup-test-"+testReplicasetUUID+".tar.zst")
	require.NoError(t, os.Mkdir(fragmentPathFor(expectedArchivePath), 0o755))

	// Another replicaset's artefacts of the same backup-id already on the node:
	// cleanup must not touch them.
	otherArchive := filepath.Join(archiveDir,
		"cleanup-test-22222222-2222-2222-2222-222222222222.tar.zst")
	require.NoError(t, os.WriteFile(otherArchive, []byte("other"), 0o644))

	_, err := Start(m, BackupStartOpts{BackupID: "cleanup-test"})
	require.Error(t, err)

	// This replicaset's archive must be removed; the other replicaset's archive
	// and the directory itself must remain.
	_, archiveStatErr := os.Stat(expectedArchivePath)
	require.True(t, os.IsNotExist(archiveStatErr),
		"failed replicaset's archive must be removed")
	_, otherStatErr := os.Stat(otherArchive)
	require.NoError(t, otherStatErr,
		"other replicaset's archive must be left intact")
	_, dirStatErr := os.Stat(archiveDir)
	require.NoError(t, dirStatErr, "backup directory must remain")
}

// TestStartBackup_packError checks an archive.Pack failure (a WAL file is a
// directory, so reading its content fails) propagates and the archive is not
// left behind.
func TestStartBackup_packError(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	// snap listed by info, but on disk it is a directory — Pack cannot read it.
	require.NoError(t, os.Mkdir(filepath.Join(walDir, "00000000000000001500.snap"), 0o755))
	writeWAL(t, walDir, "00000000000000001500.xlog", []byte("xlog"))

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}

	_, err := Start(m, BackupStartOpts{BackupID: "pack-err-bid"})
	require.Error(t, err)

	// No half-built archive left behind.
	archiveDir := filepath.Join(os.TempDir(), localBackupRootDir, "pack-err-bid")
	entries, statErr := os.ReadDir(archiveDir)
	require.NoError(t, statErr)
	for _, e := range entries {
		require.False(t, strings.HasSuffix(e.Name(), ".tar.zst"),
			"no archive must remain after Pack failure, found %q", e.Name())
	}
}

// breakMkdir makes the archive directory impossible to create by putting a
// regular file where os.MkdirAll expects a directory.
func breakMkdir(t *testing.T, backupID string) {
	t.Helper()
	root := filepath.Join(os.TempDir(), localBackupRootDir)
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, backupID), []byte("x"), 0o644))
}

// breakPack makes archive.Pack fail by turning a listed WAL file into a
// directory, which cannot be read as an archive entry.
func breakPack(t *testing.T, walDir string) {
	t.Helper()
	require.NoError(t, os.Mkdir(filepath.Join(walDir, "00000000000000001500.snap"), 0o755))
	writeWAL(t, walDir, "00000000000000001500.xlog", []byte("xlog"))
}

// TestStartBackup_stopsBackupOnFailure checks that a failure after
// box.backup.start() closes the backup again: an open backup pins the
// instance's WAL and checkpoint gc until the TTL expires.
func TestStartBackup_stopsBackupOnFailure(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, backupID, walDir string)
	}{
		{
			name: "mkdir",
			setup: func(t *testing.T, backupID, walDir string) {
				t.Helper()

				writeWalFiles(t, walDir)
				breakMkdir(t, backupID)
			},
		},
		{
			name:  "resolve files",
			setup: func(t *testing.T, backupID, walDir string) { t.Helper() }, // no WAL on disk
		},
		{
			name: "pack",
			setup: func(t *testing.T, backupID, walDir string) {
				t.Helper()

				breakPack(t, walDir)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			walDir := t.TempDir()
			tc.setup(t, "bid", walDir)

			info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
			inst := instanceMap("router-001", walDir, "")
			m := &mockEvaler{queue: startQueue(info, inst)}

			_, err := Start(m, BackupStartOpts{BackupID: "bid"})
			require.Error(t, err)
			require.True(t, slices.Contains(m.exprs, "box.backup.stop()"),
				"backup must be closed again after a failure, calls: %v", m.exprs)
		})
	}
}

// TestStartBackup_reportsFailedRollback checks that a failing box.backup.stop()
// is reported without hiding the error that triggered the rollback.
func TestStartBackup_reportsFailedRollback(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	writeWalFiles(t, walDir)
	breakMkdir(t, "bid")

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	// info -> start -> info -> instance info -> stop.
	m := &mockEvaler{
		queue: startQueue(info, inst),
		err:   errors.New("connection reset"),
		errOn: 5,
	}

	_, err := Start(m, BackupStartOpts{BackupID: "bid"})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to create archive directory")
	require.ErrorContains(t, err, "connection reset")
}

// TestStartBackup_rejectsUnsafeBackupID checks that an id which is not a single
// safe path component is refused before the instance is touched. The id names
// both the archive directory and the file base name below it, so a traversal
// writes the archive outside the backup root, and a nested id fails halfway
// with box.backup already open.
func TestStartBackup_rejectsUnsafeBackupID(t *testing.T) {
	for _, tc := range unsafeBackupIDs {
		t.Run(tc.name, func(t *testing.T) {
			base, root, tmpDir := backupIDSandbox(t)
			walDir := t.TempDir()
			writeWalFiles(t, walDir)

			info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
			inst := instanceMap("router-001", walDir, "")
			m := &mockEvaler{queue: startQueue(info, inst)}

			_, err := Start(m, BackupStartOpts{BackupID: tc.id})
			require.ErrorIs(t, err, ErrInvalidBackupID)
			require.Empty(t, m.exprs, "the instance must not be touched")
			requireSandboxIntact(t, base, root, tmpDir)
		})
	}
}

// TestStartBackup_resolveFilesSplitDirs checks resolveFiles finds files in
// memtx_dir, wal_dir, and vinyl_dir instead of assuming a single data dir.
func TestStartBackup_resolveFilesSplitDirs(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	memtxDir := t.TempDir()
	vinylDir := t.TempDir()
	vinylFile := "00000000000000001500.run"
	writeWAL(t, memtxDir, "00000000000000001500.snap", []byte("snap"))
	writeWAL(t, walDir, "00000000000000001500.xlog", []byte("xlog"))
	writeWAL(t, vinylDir, vinylFile, []byte("vinyl"))

	files := append(slices.Clone(walFiles), vinylFile)
	info := infoMap(files, nil, Vclock{1: 1502, 2: 230}, nil)
	inst := instanceMap("router-001", walDir, memtxDir)
	inst["vinyl_dir"] = vinylDir
	m := &mockEvaler{queue: startQueue(info, inst)}

	archivePath, err := Start(m, BackupStartOpts{BackupID: "split-bid"})
	require.NoError(t, err)

	entries := readArchiveEntries(t, archivePath)
	require.Equal(t, []byte("snap"), entries["00000000000000001500.snap"])
	require.Equal(t, []byte("xlog"), entries["00000000000000001500.xlog"])
	require.Equal(t, []byte("vinyl"), entries[vinylFile])
}

// TestStartBackup_resolveFilesNestedVinylPath checks a vinyl .run file nested
// under <vinyl_dir>/<space_id>/<index_id>/ -- reported as an absolute path,
// the Tarantool 3.8+ shape -- keeps that subdirectory structure inside the
// archive instead of flattening to its base name.
func TestStartBackup_resolveFilesNestedVinylPath(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	walDir := t.TempDir()
	memtxDir := t.TempDir()
	vinylDir := t.TempDir()

	vinylSubdir := filepath.Join(vinylDir, "512", "0")
	require.NoError(t, os.MkdirAll(vinylSubdir, 0o755))
	vinylPath := filepath.Join(vinylSubdir, "00000000000000001500.run")
	require.NoError(t, os.WriteFile(vinylPath, []byte("vinyl"), 0o644))
	writeWAL(t, memtxDir, "00000000000000001500.snap", []byte("snap"))
	writeWAL(t, walDir, "00000000000000001500.xlog", []byte("xlog"))

	files := append(slices.Clone(walFiles), vinylPath)
	info := infoMap(files, nil, Vclock{1: 1502, 2: 230}, nil)
	inst := instanceMap("router-001", walDir, memtxDir)
	inst["vinyl_dir"] = vinylDir
	m := &mockEvaler{queue: startQueue(info, inst)}

	archivePath, err := Start(m, BackupStartOpts{BackupID: "nested-vinyl-bid"})
	require.NoError(t, err)

	entries := readArchiveEntries(t, archivePath)
	require.Equal(t, []byte("snap"), entries["00000000000000001500.snap"])
	require.Equal(t, []byte("xlog"), entries["00000000000000001500.xlog"])
	require.Equal(t, []byte("vinyl"), entries["512/0/00000000000000001500.run"])

	fragment, err := os.ReadFile(fragmentPathFor(archivePath))
	require.NoError(t, err)
	var parsed Fragment
	require.NoError(t, json.Unmarshal(fragment, &parsed))
	require.Contains(t, parsed.Files, "512/0/00000000000000001500.run")
}
