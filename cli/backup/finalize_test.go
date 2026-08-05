package backup

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStop_closesAndRemovesOnlyOwnArtifacts(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const backupID = "stop-bid"
	backupDir := filepath.Join(os.TempDir(), localBackupRootDir, backupID)
	require.NoError(t, os.MkdirAll(backupDir, 0o755))

	basePath := filepath.Join(backupDir, backupID+"-"+testReplicasetUUID)
	archivePath := basePath + ".tar.zst"
	fragmentPath := basePath + ".json"
	require.NoError(t, os.WriteFile(archivePath, []byte("own archive"), 0o644))
	require.NoError(t, os.WriteFile(fragmentPath, []byte("own fragment"), 0o644))

	const otherReplicasetUUID = "22222222-2222-2222-2222-222222222222"
	otherBasePath := filepath.Join(backupDir, backupID+"-"+otherReplicasetUUID)
	otherArchive := otherBasePath + ".tar.zst"
	otherFragment := otherBasePath + ".json"
	require.NoError(t, os.WriteFile(otherArchive, []byte("other archive"), 0o644))
	require.NoError(t, os.WriteFile(otherFragment, []byte("other fragment"), 0o644))

	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	inst := instanceMap("router-001", "", "")
	m := &mockEvaler{queue: [][]any{{info}, nil, {inst}}}

	require.NoError(t, Stop(m, backupID))
	require.True(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must be called")
	require.NoFileExists(t, archivePath)
	require.NoFileExists(t, fragmentPath)
	require.FileExists(t, otherArchive)
	require.FileExists(t, otherFragment)
	require.DirExists(t, backupDir, "shared directory must remain while it has other artifacts")
}

func TestStop_alreadyClosedRemovesStaleOwnArtifactsAndEmptyDir(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const backupID = "stale-bid"
	backupDir := filepath.Join(os.TempDir(), localBackupRootDir, backupID)
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	basePath := filepath.Join(backupDir, backupID+"-"+testReplicasetUUID)
	require.NoError(t, os.WriteFile(basePath+".tar.zst", []byte("stale archive"), 0o644))
	require.NoError(t, os.WriteFile(basePath+".json", []byte("stale fragment"), 0o644))

	inst := instanceMap("router-001", "", "")
	m := &mockEvaler{queue: [][]any{nil, {inst}}}

	require.NoError(t, Stop(m, backupID))
	require.False(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must not be called")
	require.NoDirExists(t, backupDir)
}

// TestStop_emptyBackupIDIsRejected replaces TestStop_noBackupID, which pinned
// the leak: an unexpanded ${BACKUP_ID} closed the lease on every node, exited 0
// and stranded a full archive per shard that nothing ever reclaims.
func TestStop_emptyBackupIDIsRejected(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const backupID = "empty-id"
	backupDir := filepath.Join(os.TempDir(), localBackupRootDir, backupID)
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	archivePath := filepath.Join(backupDir, backupID+"-"+testReplicasetUUID+".tar.zst")
	require.NoError(t, os.WriteFile(archivePath, []byte("archive"), 0o644))

	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{queue: [][]any{{info}, nil}}

	err := Stop(m, "")
	require.ErrorIs(t, err, ErrInvalidBackupID)
	require.ErrorContains(t, err, `""`, "the error must name the id it rejected")
	require.False(t, slices.Contains(m.exprs, "box.backup.stop()"),
		"the lease must survive a malformed invocation")
	require.FileExists(t, archivePath, "artifacts must remain for a later retry")
}

// TestStop_rejectsUnsafeBackupID checks the id is refused before box.backup is
// closed. Finalize unlinks files and rmdirs a directory both named by the id,
// so a traversal deletes paths outside the backup root.
func TestStop_rejectsUnsafeBackupID(t *testing.T) {
	for _, tc := range unsafeBackupIDs {
		t.Run(tc.name, func(t *testing.T) {
			base, root, tmpDir := backupIDSandbox(t)

			info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
			inst := instanceMap("router-001", "", "")
			m := &mockEvaler{queue: [][]any{{info}, nil, {inst}}}

			err := Stop(m, tc.id)
			require.ErrorIs(t, err, ErrInvalidBackupID)
			require.Empty(t, m.exprs, "the instance must not be touched")
			requireSandboxIntact(t, base, root, tmpDir)
		})
	}
}

func TestStop_infoError(t *testing.T) {
	m := &mockEvaler{err: errors.New("boom"), errOn: 1}

	err := Stop(m, "info-err-bid")
	require.ErrorContains(t, err, "boom")
}

func TestStop_stopErrorLeavesArtifacts(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const backupID = "stop-err-bid"
	backupDir := filepath.Join(os.TempDir(), localBackupRootDir, backupID)
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	archivePath := filepath.Join(backupDir, backupID+"-"+testReplicasetUUID+".tar.zst")
	require.NoError(t, os.WriteFile(archivePath, []byte("archive"), 0o644))

	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{err: errors.New("boom"), errOn: 2, queue: [][]any{{info}}}

	err := Stop(m, backupID)
	require.ErrorContains(t, err, "boom")
	require.FileExists(t, archivePath, "artifacts must remain after stop failure")
}

func TestStop_instanceInfoErrorLeavesArtifacts(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const backupID = "info-err-bid"
	backupDir := filepath.Join(os.TempDir(), localBackupRootDir, backupID)
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	archivePath := filepath.Join(backupDir, backupID+"-"+testReplicasetUUID+".tar.zst")
	require.NoError(t, os.WriteFile(archivePath, []byte("archive"), 0o644))

	m := &mockEvaler{err: errors.New("boom"), errOn: 2, queue: [][]any{nil}}

	err := Stop(m, backupID)
	require.ErrorContains(t, err, "failed to resolve instance metadata")
	require.FileExists(t, archivePath)
}

func TestStopForce_closesOpenBackupAndTouchesNoFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const backupID = "force-bid"
	backupDir := filepath.Join(os.TempDir(), localBackupRootDir, backupID)
	require.NoError(t, os.MkdirAll(backupDir, 0o755))
	basePath := filepath.Join(backupDir, backupID+"-"+testReplicasetUUID)
	archivePath := basePath + ".tar.zst"
	fragmentPath := basePath + ".json"
	require.NoError(t, os.WriteFile(archivePath, []byte("archive"), 0o644))
	require.NoError(t, os.WriteFile(fragmentPath, []byte("fragment"), 0o644))

	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{queue: [][]any{{info}, nil}}

	require.NoError(t, CloseIfOpen(m))
	require.True(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must be called")
	require.FileExists(t, archivePath, "force must remove no local artifact")
	require.FileExists(t, fragmentPath, "force must remove no local artifact")
}

func TestStopForce_noBackupOpenIsANoop(t *testing.T) {
	m := &mockEvaler{queue: [][]any{nil}}

	require.NoError(t, CloseIfOpen(m))
	require.False(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must not be called")
}

func TestStopForce_infoError(t *testing.T) {
	m := &mockEvaler{err: errors.New("boom"), errOn: 1}

	err := CloseIfOpen(m)
	require.ErrorContains(t, err, "boom")
}

func TestStopForce_stopErrorPropagates(t *testing.T) {
	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{err: errors.New("boom"), errOn: 2, queue: [][]any{{info}}}

	err := CloseIfOpen(m)
	require.ErrorContains(t, err, "boom")
}
