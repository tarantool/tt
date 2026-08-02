package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tarantool/tt/cli/backup/storage"
)

// LatestManifest reads the manifest with the lexicographically greatest storage key.
// Backup IDs are sortable, so there is no need to load and build every backup chain.
func LatestManifest(ctx context.Context, store storage.Storage) (*ClusterManifest, error) {
	objects, err := store.List(ctx, storage.ManifestsPrefix())
	if err != nil {
		return nil, fmt.Errorf("list backup manifests: %w", err)
	}
	if len(objects) == 0 {
		return nil, nil
	}

	latest := objects[len(objects)-1]
	data, err := storage.GetBytes(ctx, store, latest.Key)
	if err != nil {
		return nil, fmt.Errorf("read latest backup manifest %q: %w", latest.Key, err)
	}

	var manifest ClusterManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode latest backup manifest %q: %w", latest.Key, err)
	}

	return &manifest, nil
}
