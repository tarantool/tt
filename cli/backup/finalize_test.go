package backup

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStop_closesAndRemovesBackupDir(t *testing.T) {
	// Pre-create the backup directory with an archive and a fragment, to verify
	// Stop wipes both.
	backupDir := filepath.Join(os.TempDir(), "tt-backup", "stop-bid")
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(backupDir, "stop-bid-"+testReplicasetUUID+".tar.zst"),
		[]byte("z"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(backupDir, "stop-bid-"+testReplicasetUUID+".json"),
		[]byte("{}"), 0o644))

	// GetInfo: backup is open; stop: nil.
	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{queue: [][]any{{info}, nil}}

	require.NoError(t, Stop(m, "stop-bid"))

	require.True(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must be called")
	_, err := os.Stat(backupDir)
	require.True(t, os.IsNotExist(err), "backup directory must be removed")
}

// TestStop_idempotentWhenAlreadyClosed checks a repeated finalize is a no-op:
// no stop() call when the backup is already closed, and a missing backup
// directory is not an error.
func TestStop_idempotentWhenAlreadyClosed(t *testing.T) {
	// GetInfo: no backup open → nil.
	m := &mockEvaler{queue: [][]any{nil}}

	require.NoError(t, Stop(m, "missing-bid"))

	require.False(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must not be called")
}

// TestStop_noBackupID checks an empty backup-id skips local removal but still
// closes the backup.
func TestStop_noBackupID(t *testing.T) {
	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{queue: [][]any{{info}, nil}}

	require.NoError(t, Stop(m, ""))
	require.True(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must be called")
}

// TestStop_infoError checks a box.backup.info() error propagates.
func TestStop_infoError(t *testing.T) {
	m := &mockEvaler{err: errors.New("boom"), errOn: 1}

	err := Stop(m, "")
	require.ErrorContains(t, err, "boom")
}

// TestStop_stopError checks a box.backup.stop() error propagates and the local
// directory is not touched.
func TestStop_stopError(t *testing.T) {
	backupDir := filepath.Join(os.TempDir(), "tt-backup", "stop-err-bid")
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "a.tar.zst"), []byte("z"), 0o644))

	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{err: errors.New("boom"), errOn: 2} // stop() fails
	m.queue = [][]any{{info}}

	err := Stop(m, "stop-err-bid")
	require.ErrorContains(t, err, "boom")

	// Directory untouched: stop failed, do not remove local artefacts.
	_, statErr := os.Stat(backupDir)
	require.NoError(t, statErr, "backup directory must remain after stop failure")
	_ = os.RemoveAll(backupDir) // cleanup
}
