package backup

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// fullStart runs a full backup start over the standard queue, configured by
// configure, returning the mock and the error.
func fullStart(t *testing.T, configure func(*mockEvaler)) (*mockEvaler, error) {
	t.Helper()

	return fullStartWith(t, BackupStartOpts{BackupID: "bid"}, configure)
}

// fullStartWith is fullStart with the start options given.
func fullStartWith(
	t *testing.T, opts BackupStartOpts, configure func(*mockEvaler),
) (*mockEvaler, error) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())

	walDir := t.TempDir()
	writeWalFiles(t, walDir)

	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	m := &mockEvaler{queue: startQueue(info, inst)}
	configure(m)

	_, err := Start(m, opts)

	return m, err
}

// indexOf returns the position of expr in the ordered call log, or -1.
func indexOf(calls []string, expr string) int {
	return slices.Index(calls, expr)
}

// TestStartBackup_fullTakesNoCheckpointByDefault checks that without
// --snapshot a full backup ships the newest checkpoint as it is: nothing reads
// it and no box.snapshot() is taken.
func TestStartBackup_fullTakesNoCheckpointByDefault(t *testing.T) {
	m, err := fullStart(t, func(*mockEvaler) {})
	require.NoError(t, err)

	require.Empty(t, m.side, "no checkpoint is taken: %v", m.all)
	require.True(t, slices.Contains(m.all, "box.backup.start(...)"), m.all)
}

// TestStartBackup_incrementalTakesNoCheckpoint checks that an increment, which
// ships journals only, takes no checkpoint.
func TestStartBackup_incrementalTakesNoCheckpoint(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	walDir := t.TempDir()
	writeWAL(t, walDir, "00000000000000001500.xlog", []byte("xlog"))

	files := []string{"00000000000000001500.xlog"}
	info := infoMap(files, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", walDir, "")
	mock := &mockEvaler{queue: [][]any{
		nil,
		nil,
		{info},
		{inst},
		{[]any{[]any{int8(1), testInstanceUUID, "router-001"}}},
	}}

	_, err := Start(mock, BackupStartOpts{BackupID: "bid", FromVclock: Vclock{1: 1500}})
	require.NoError(t, err)

	require.Empty(t, mock.side, "an increment ships no checkpoint")
}

// TestStartBackup_snapshotFlagTakesCheckpoint checks that --snapshot takes a
// checkpoint before box.backup.start picks the newest one up.
func TestStartBackup_snapshotFlagTakesCheckpoint(t *testing.T) {
	mock, err := fullStartWith(t, BackupStartOpts{BackupID: "bid", Snapshot: true},
		func(*mockEvaler) {})
	require.NoError(t, err)

	require.Equal(t, []string{snapshotExpr}, mock.side,
		"a checkpoint is taken: %v", mock.all)
	require.Less(t, indexOf(mock.all, snapshotExpr), indexOf(mock.all, "box.backup.start(...)"),
		"the checkpoint is taken before box.backup.start: %v", mock.all)
}

// TestStartBackup_snapshotFlagStopsWhenSnapshotFails checks that a checkpoint
// --snapshot asks for and cannot get stops the backup before it opens.
func TestStartBackup_snapshotFlagStopsWhenSnapshotFails(t *testing.T) {
	mock, err := fullStartWith(t, BackupStartOpts{BackupID: "bid", Snapshot: true},
		func(m *mockEvaler) {
			m.snapshotErr = errors.New("Snapshot is already in progress")
		})
	require.ErrorContains(t, err, "Snapshot is already in progress")

	require.False(t, slices.Contains(mock.all, "box.backup.start(...)"),
		"no backup is opened: %v", mock.all)
}

// TestStartBackup_snapshotFlagRefusedForIncremental checks that --snapshot
// with an increment, which ships no checkpoint, is refused before the
// instance is asked anything.
func TestStartBackup_snapshotFlagRefusedForIncremental(t *testing.T) {
	mock := &mockEvaler{}

	_, err := Start(mock, BackupStartOpts{
		BackupID:   "bid",
		FromVclock: Vclock{1: 1500},
		Snapshot:   true,
	})
	require.ErrorContains(t, err, "full backup only")
	require.Empty(t, mock.all, "the instance is not asked anything")
}

// TestStartBackup_snapshotFlagAcceptsConcurrentCheckpoint checks that with
// --snapshot a checkpoint completed by someone else while box.snapshot() waited
// stands in for its own, and the backup opens after it.
func TestStartBackup_snapshotFlagAcceptsConcurrentCheckpoint(t *testing.T) {
	handler := captureLog(t)

	mock, err := fullStartWith(t, BackupStartOpts{BackupID: "bid", Snapshot: true},
		func(m *mockEvaler) {
			m.snapshotOutcomes = []string{"concurrent"}
		})
	require.NoError(t, err)

	require.Equal(t, []string{snapshotExpr}, mock.side, mock.all)
	require.Less(t, indexOf(mock.all, snapshotExpr), indexOf(mock.all, "box.backup.start(...)"),
		"the backup opens after the checkpoint completes: %v", mock.all)
	require.Contains(t, logMessages(handler), "already in progress")
}

// TestSnapshotCheckpoint_rejectsAnUnknownOutcome checks that an answer the
// snapshot expression never gives is an error, not a checkpoint taken.
func TestSnapshotCheckpoint_rejectsAnUnknownOutcome(t *testing.T) {
	m := &mockEvaler{snapshotOutcomes: []string{"maybe"}}

	err := snapshotCheckpoint(m)
	require.ErrorContains(t, err, "unexpected checkpoint outcome")
}
