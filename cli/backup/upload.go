package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup/storage"
)

// ArchiveToUpload pairs a local archive file with its storage destination.
type ArchiveToUpload struct {
	LocalPath      string
	StorageKey     string
	Size           int64
	ReplicasetUUID string
}

// Upload uploads archives and the manifest to storage.
// If the upload fails, all previously uploaded
// archives are deleted.
func Upload(
	ctx context.Context,
	store storage.Storage,
	keyPrefix string,
	backupID BackupID,
	manifestData []byte,
	archives []ArchiveToUpload,
) error {
	uploadedKeys := make([]string, 0, len(archives))

	for _, archive := range archives {
		if err := putFile(ctx, store, archive.StorageKey,
			archive.LocalPath, archive.Size); err != nil {
			deleteObjects(ctx, store, uploadedKeys)
			return fmt.Errorf("upload archive for replicaset %q: %w", archive.ReplicasetUUID, err)
		}
		uploadedKeys = append(uploadedKeys, archive.StorageKey)
	}

	manifestKey := keyPrefix + storage.ManifestKey(string(backupID))
	if err := storage.PutBytes(ctx, store, manifestKey, manifestData); err != nil {
		deleteObjects(ctx, store, uploadedKeys)
		return fmt.Errorf("upload manifest: %w", err)
	}

	return nil
}

// PrepareArchives stats each local archive, extracts the replicaset UUID from
// its filename, and computes the storage key. Returns a slice ready for Upload
// and a map of replicaset_uuid → ArtifactLocation for manifest building.
//
// The two keys differ: an object is PUT under the full key, keyPrefix
// included, while the manifest records the key relative to the storage root it
// is stored beside. That is what every reader resolves an artifact path
// against, and a manifest carrying its own prefix sends them looking for
// <prefix>/<prefix>/data/…: restore cannot download the archive at all, verify
// reports the live one missing and the stored one dangling, and gc counts the
// stored one as an orphan (its re-check of the manifest is what keeps it from
// being deleted).
func PrepareArchives(
	paths []string,
	keyPrefix string,
	backupID BackupID,
) ([]ArchiveToUpload, map[string]*ArtifactLocation, error) {
	archives := make([]ArchiveToUpload, 0, len(paths))
	locations := make(map[string]*ArtifactLocation, len(paths))

	for _, archivePath := range paths {
		info, err := os.Stat(archivePath)
		if err != nil {
			return nil, nil, fmt.Errorf("stat archive %q: %w", archivePath, err)
		}

		replicasetUUID, err := uuidFromArchivePath(archivePath, string(backupID))
		if err != nil {
			return nil, nil, fmt.Errorf("archive %q: %w", archivePath, err)
		}

		if _, exists := locations[replicasetUUID]; exists {
			return nil, nil, fmt.Errorf("duplicate archive for replicaset %q", replicasetUUID)
		}

		relativeKey := storage.ArchiveKey(string(backupID), replicasetUUID)

		archives = append(archives, ArchiveToUpload{
			LocalPath:      archivePath,
			StorageKey:     keyPrefix + relativeKey,
			Size:           info.Size(),
			ReplicasetUUID: replicasetUUID,
		})
		locations[replicasetUUID] = &ArtifactLocation{
			Path:      relativeKey,
			SizeBytes: info.Size(),
		}
	}

	return archives, locations, nil
}

// StoragePrefix builds a storage key prefix from a cluster name and an
// optional environment. Returns "" when clusterName is empty, so keys never
// start with a leading or doubled slash.
func StoragePrefix(clusterName, environment string) string {
	if clusterName == "" {
		return ""
	}

	return storage.PrefixWithSlash(path.Join(clusterName, environment))
}

// BuildTopologyFromFragments builds a Topology from the collected fragments.
// Each fragment represents the master instance that produced the backup.
func BuildTopologyFromFragments(fragments []*Fragment) Topology {
	replicasets := make(map[string][]TopologyInstance, len(fragments))
	for _, fragment := range fragments {
		replicasets[fragment.ReplicasetUUID] = append(
			replicasets[fragment.ReplicasetUUID],
			TopologyInstance{
				InstanceUUID: fragment.InstanceUUID,
				InstanceName: fragment.InstanceName,
				Hostname:     fragment.Hostname,
			},
		)
	}
	return Topology{Replicasets: replicasets}
}

// ReadPlan reads and decodes a tt backup plan JSON file.
func ReadPlan(filePath string) (*BackupPlan, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read plan %q: %w", filePath, err)
	}

	var plan BackupPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("decode plan %q: %w", filePath, err)
	}

	return &plan, nil
}

// ValidateFragmentsAgainstPlan checks that every replicaset expected by the
// plan has a matching fragment.
func ValidateFragmentsAgainstPlan(fragments []*Fragment, plan *BackupPlan) error {
	covered := make(map[string]bool, len(fragments))
	for _, fragment := range fragments {
		covered[fragment.ReplicasetUUID] = true
	}

	var missing []string
	for replicasetUUID := range plan.Replicasets {
		if !covered[replicasetUUID] {
			missing = append(missing, replicasetUUID)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return fmt.Errorf("plan expects %d replicasets but fragments are missing for: %s",
			len(plan.Replicasets), strings.Join(missing, ", "))
	}

	return nil
}

// SplitPaths splits a comma-separated list of paths and trims whitespace.
func SplitPaths(commaSeparated string) []string {
	if commaSeparated == "" {
		return nil
	}

	paths := strings.Split(commaSeparated, ",")
	for i := range paths {
		paths[i] = strings.TrimSpace(paths[i])
	}

	return paths
}

// ReadFragments reads and decodes per-shard instance_backup.json files.
func ReadFragments(paths []string) ([]*Fragment, error) {
	fragments := make([]*Fragment, 0, len(paths))
	for _, filePath := range paths {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read fragment %q: %w", filePath, err)
		}

		fragment, err := DecodeFragment(data)
		if err != nil {
			return nil, fmt.Errorf("read fragment %q: %w", filePath, err)
		}

		fragments = append(fragments, fragment)
	}

	return fragments, nil
}

// uuidFromArchivePath extracts the replicaset UUID from an archive filename.
// The expected filename format is <backup-id>-<replicaset_uuid>.tar.zst.
func uuidFromArchivePath(archivePath, backupID string) (string, error) {
	baseName := filepath.Base(archivePath)

	name := strings.TrimSuffix(baseName, ".tar.zst")
	if name == baseName {
		return "", fmt.Errorf("file does not have .tar.zst extension")
	}

	prefix := backupID + "-"
	if !strings.HasPrefix(name, prefix) {
		return "", fmt.Errorf(
			"archive filename %q does not start with backup-id %q", baseName, backupID)
	}

	replicasetUUID := strings.TrimPrefix(name, prefix)
	if replicasetUUID == "" {
		return "", fmt.Errorf("empty replicaset UUID in filename %q", baseName)
	}

	return replicasetUUID, nil
}

// putFile uploads the local file at filePath to the storage under key.
func putFile(ctx context.Context, store storage.Storage, key, filePath string, size int64) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %q: %w", filePath, err)
	}
	defer file.Close()

	if err := store.Put(ctx, key, file, size); err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}

	return nil
}

// deleteObjects best-effort deletes the given keys from storage. Errors are
// ignored: the primary failure has already been reported, and orphaned
// archives without a manifest are harmless.
func deleteObjects(ctx context.Context, store storage.Storage, keys []string) {
	for _, key := range keys {
		_ = store.Delete(ctx, key)
	}
}
