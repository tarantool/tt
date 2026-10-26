package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/restore"
)

// withRestoreApplyFlags sets the directory flags of tt restore apply for the
// test.
func withRestoreApplyFlags(t *testing.T, cfg, instance, workDir, walDir string) {
	t.Helper()

	previous := []string{
		restoreApplyCfg, restoreApplyInstance, restoreApplyWorkDir,
		restoreApplySnapshotDir, restoreApplyWALDir, restoreApplyVinylDir,
	}

	t.Cleanup(func() {
		restoreApplyCfg, restoreApplyInstance, restoreApplyWorkDir = previous[0],
			previous[1], previous[2]
		restoreApplySnapshotDir, restoreApplyWALDir, restoreApplyVinylDir = previous[3],
			previous[4], previous[5]
	})

	restoreApplyCfg, restoreApplyInstance, restoreApplyWorkDir = cfg, instance, workDir
	restoreApplySnapshotDir, restoreApplyWALDir, restoreApplyVinylDir = "", walDir, ""
}

// TestTargetRestoreDestination pins where tt restore apply <APP>:<INSTANCE>
// puts the files: the directories tt runs the instance with, a per-directory
// flag over each, with the application directory as work_dir.
//
//nolint:paralleltest // Sets the flags of the command.
func TestTargetRestoreDestination(t *testing.T) {
	externalProject(t)

	withRestoreApplyFlags(t, "", "", "", "")

	dest, err := targetRestoreDestination("myapp:storage-1")
	require.NoError(t, err)

	dataDir := filepath.Join(projectDir, ".tarantool", "var", "lib", "myapp", "storage-1")
	assert.Equal(t, restore.Layout{Snapshot: dataDir, WAL: dataDir, Vinyl: dataDir}, dest.layout)
	assert.Equal(t, "instance myapp:storage-1", dest.sources.Snapshot)
	assert.Equal(t, projectDir, dest.workDir)
	assert.Equal(t, "storage-1", dest.instance)

	walDir := t.TempDir()

	withRestoreApplyFlags(t, "", "", "", walDir)

	dest, err = targetRestoreDestination("myapp:storage-1")
	require.NoError(t, err)
	assert.Equal(t, restore.Layout{Snapshot: dataDir, WAL: walDir, Vinyl: dataDir}, dest.layout)
	assert.Equal(t, "--wal-dir", dest.sources.WAL)
}

// TestTargetRestoreDestinationRefuses pins the targets tt restore apply
// refuses as rejected input: one with the flags of an instance tt does not
// run, one that selects several instances, and an external process.
//
//nolint:paralleltest // Sets the flags of the command.
func TestTargetRestoreDestinationRefuses(t *testing.T) {
	externalProject(t)

	for _, flags := range [][3]string{
		{"config.yaml", "", ""}, {"", "storage-1", ""}, {"", "", "/srv"},
	} {
		withRestoreApplyFlags(t, flags[0], flags[1], flags[2], "")

		_, err := targetRestoreDestination("myapp:storage-1")
		require.ErrorIs(t, err, restore.ErrValidation, flags)
	}

	withRestoreApplyFlags(t, "", "", "", "")

	for _, target := range []string{"myapp:*", "myapp:worker-1", "myapp:missing"} {
		_, err := targetRestoreDestination(target)
		require.ErrorIs(t, err, restore.ErrValidation, target)
	}
}
