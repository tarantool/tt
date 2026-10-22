package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/tarantool/tt/sdk/backup/storage"
)

var (
	errLastLatestBackupManifest = errors.New("latest backup manifest")
)

// LatestManifest reads the manifest with the lexicographically greatest
// manifest key. Backup IDs are sortable, so there is no need to load and build
// every backup chain. Anything under manifests/ that is not a manifest key (an
// operator note, an S3 console folder placeholder) is skipped rather than
// decoded, and the manifest that is returned has to be usable: callers chain
// the next backup onto the backup id they read here.
//
//nolint:nilnil // An empty storage has no latest manifest.
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

	unmarshalErr := json.Unmarshal(data, &manifest)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("decode latest backup manifest %q: %w", key, unmarshalErr)
	}

	validateErr := manifest.Validate()
	if validateErr != nil {
		return nil, fmt.Errorf("invalid latest backup manifest %q: %w", key, validateErr)
	}

	// The key alone decides which manifest is the latest one, so a key naming
	// another backup makes this answer disagree with a loaded chain, which
	// orders by backup_id.
	if string(manifest.BackupID) != backupID {
		return nil, fmt.Errorf(
			"%w %q holds backup_id %q", errLastLatestBackupManifest,
			key, manifest.BackupID)
	}

	return &manifest, nil
}

// latestManifestKey returns the greatest manifest key among objects, which are
// sorted by key, together with the backup id that key names.
func latestManifestKey(objects []storage.ObjectInfo) (string, string, bool) {
	for _, object := range slices.Backward(objects) {
		if id, isManifest := storage.ManifestBackupID(object.Key); isManifest {
			return object.Key, id, true
		}
	}

	return "", "", false
}
