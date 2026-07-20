package backup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/connector"
)

// Stop closes box.backup on the instance and removes only this replicaset's
// local archive and manifest fragment. Stop is idempotent: if the backup is already closed,
// stop() is skipped and stale local artifacts are still removed.
func Stop(conn connector.Connector, backupID string) error {
	info, err := GetInfo(conn)
	if err != nil {
		return fmt.Errorf("failed to check backup state: %w", err)
	}
	if info != nil {
		if err := stopBackup(conn); err != nil {
			return fmt.Errorf("finalize: %w", err)
		}
	}

	if backupID == "" {
		return nil
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
