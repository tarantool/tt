package backup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/connector"
)

// Stop closes box.backup on the instance and removes only this replicaset's
// local archive and manifest fragment. Stop is idempotent: if the backup is
// already closed, stop() is skipped and stale local artifacts are still
// removed. A backupID that is not a safe path component (see ValidateBackupID)
// is rejected before the instance is touched.
func Stop(conn connector.Connector, backupID string) error {
	// Both the artifacts to unlink and the directory to rmdir are named by the
	// id, so it is checked before box.backup.stop() is issued: a run that
	// released the lease and then refused to clean up would strand the archive
	// with no way to finish the backup.
	if err := ValidateBackupID(backupID); err != nil {
		return err //nolint:wrapcheck
	}

	if err := CloseIfOpen(conn); err != nil {
		return fmt.Errorf("failed to close backup: %w", err)
	}

	inst, err := GetInstanceInfo(conn)
	if err != nil {
		return fmt.Errorf("failed to resolve instance metadata: %w", err)
	}

	backupDir := filepath.Join(os.TempDir(), localBackupRootDir, backupID)
	basePath := filepath.Join(backupDir, backupID+"-"+inst.ReplicasetUUID)
	archivePath := basePath + ".tar.zst"
	fragmentPath := basePath + ".json"

	for _, path := range []string{archivePath, fragmentPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove backup artifact %q: %w", path, err)
		}
	}

	// Delete the backup directory if it is empty.
	entries, err := os.ReadDir(backupDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read backup directory %q: %w", backupDir, err)
	}

	if len(entries) == 0 {
		if err := os.Remove(backupDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove empty backup directory %q: %w", backupDir, err)
		}
	}

	return nil
}

// CloseIfOpen calls box.backup.stop() if a backup is currently open, and does
// nothing if it is not.
func CloseIfOpen(conn connector.Connector) error {
	info, err := GetInfo(conn)
	if err != nil {
		return fmt.Errorf("failed to check backup state: %w", err)
	}

	if info == nil {
		return nil
	}

	if err := stopBackup(conn); err != nil {
		return fmt.Errorf("finalize: %w", err)
	}

	return nil
}
