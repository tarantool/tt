package backup

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/connector"
)

// Stop closes box.backup on the instance and removes the local backup directory
// (/tmp/tt-backup/<backup-id>/), including the archive and the manifest fragment
// of every replicaset on this node. It is idempotent: if the backup is already
// closed, stop() is skipped.
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

	if backupID != "" {
		backupDir := filepath.Join(os.TempDir(), "tt-backup", backupID)
		if err := os.RemoveAll(backupDir); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove backup directory %q: %w", backupDir, err)
		}
	}

	return nil
}
