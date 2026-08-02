package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tarantool/tt/cli/backup/storage"
)

// LatestManifest reads the manifest with the lexicographically greatest
// manifest key. Backup IDs are sortable, so there is no need to load and build
// every backup chain. Anything under manifests/ that is not a manifest key (an
// operator note, an S3 console folder placeholder) is skipped rather than
// decoded, and the manifest that is returned has to be usable: callers chain
// the next backup onto the backup id they read here.
func LatestManifest(ctx context.Context, store storage.Storage) (*ClusterManifest, error) {
	objects, err := store.List(ctx, storage.ManifestsPrefix())
	if err != nil {
		return nil, fmt.Errorf("list backup manifests: %w", err)
	}

	key, backupID, ok := latestManifestKey(objects)
	if !ok {
		return nil, nil
	}

	data, err := storage.GetBytes(ctx, store, key)
	if err != nil {
		return nil, fmt.Errorf("read latest backup manifest %q: %w", key, err)
	}

	var manifest ClusterManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode latest backup manifest %q: %w", key, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid latest backup manifest %q: %w", key, err)
	}
	// The key alone decides which manifest is the latest one, so a key naming
	// another backup makes this answer disagree with a loaded chain, which
	// orders by backup_id.
	if string(manifest.BackupID) != backupID {
		return nil, fmt.Errorf(
			"latest backup manifest %q holds backup_id %q",
			key, manifest.BackupID)
	}

	return &manifest, nil
}

// latestManifestKey returns the greatest manifest key among objects, which are
// sorted by key, together with the backup id that key names.
func latestManifestKey(objects []storage.ObjectInfo) (key, backupID string, ok bool) {
	for i := len(objects) - 1; i >= 0; i-- {
		if id, isManifest := storage.ManifestBackupID(objects[i].Key); isManifest {
			return objects[i].Key, id, true
		}
	}

	return "", "", false
}
