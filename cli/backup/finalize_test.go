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

func TestStop_noBackupID(t *testing.T) {
	info := infoMap(walFiles, Vclock{1: 1500}, Vclock{1: 1502}, nil)
	m := &mockEvaler{queue: [][]any{{info}, nil}}

	require.NoError(t, Stop(m, ""))
	require.True(t, slices.Contains(m.exprs, "box.backup.stop()"), "stop must be called")
}

func TestStop_infoError(t *testing.T) {
	m := &mockEvaler{err: errors.New("boom"), errOn: 1}

	err := Stop(m, "")
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
