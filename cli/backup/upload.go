package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup/archive"
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
	backupID BackupID,
	manifestData []byte,
	archives []ArchiveToUpload,
) error {
	uploadedKeys := make([]string, 0, len(archives))

	// Named item, not archive: the archive package is imported in this file.
	for _, item := range archives {
		if err := putFile(ctx, store, item.StorageKey,
			item.LocalPath, item.Size); err != nil {
			deleteObjects(ctx, store, uploadedKeys)
			return fmt.Errorf("upload archive for replicaset %q: %w", item.ReplicasetUUID, err)
		}
		uploadedKeys = append(uploadedKeys, item.StorageKey)
	}

	manifestKey := storage.ManifestKey(string(backupID))
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
// Keys are relative to the storage root, which is where the manifest stored
// beside them says they are and what every reader resolves them against. The
// <cluster_name>/<environment>/ segment of the layout belongs to the storage
// the objects are written into (StorageConfig.Scope), not to the keys: a
// manifest carrying its own prefix sends readers looking for
// <prefix>/<prefix>/data/…, where restore cannot download the archive at all,
// verify reports the live one missing and the stored one dangling, and gc
// counts the stored one as an orphan.
func PrepareArchives(
	paths []string,
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

		key := storage.ArchiveKey(string(backupID), replicasetUUID)

		archives = append(archives, ArchiveToUpload{
			LocalPath:      archivePath,
			StorageKey:     key,
			Size:           info.Size(),
			ReplicasetUUID: replicasetUUID,
		})
		locations[replicasetUUID] = &ArtifactLocation{
			Path:      key,
			SizeBytes: info.Size(),
		}
	}

	return archives, locations, nil
}

// VerifyArchives recomputes the sha256 of every archive about to be uploaded
// and checks it against the fragment describing the same replicaset. The
// fragment's checksum was computed on the node, before the archive crossed the
// network to this host: a copy that went wrong in between is otherwise stored
// and published as healthy, and found out at restore time.
//
// A fragment that carries no checksum gets the computed one, so the manifest
// describes the archive either way. Those replicaset UUIDs are returned rather
// than silently accepted: nothing was verified for them, and the caller says so.
//
// An archive no fragment describes is refused rather than skipped. Today the
// caller cannot produce one -- the counts have to match, neither side may hold
// a duplicate replicaset, and every fragment needs an archive -- but that is an
// invariant of three other checks, and a function that verifies archives must
// not report success on one it never looked at.
//
// The fragment ends up holding the checksum computed here, in the canonical
// lower-case form, so the manifest records what this host actually read.
func VerifyArchives(archives []ArchiveToUpload, fragments []*Fragment) ([]string, error) {
	byReplicaset := make(map[string]*Fragment, len(fragments))
	for _, fragment := range fragments {
		byReplicaset[fragment.ReplicasetUUID] = fragment
	}

	unverified := make([]string, 0)

	for _, item := range archives {
		fragment, ok := byReplicaset[item.ReplicasetUUID]
		if !ok {
			return nil, fmt.Errorf(
				"no fragment describes archive %q of replicaset %s",
				item.LocalPath, item.ReplicasetUUID)
		}

		checksum, err := archive.Checksum(item.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("checksum archive %q: %w", item.LocalPath, err)
		}

		switch {
		case fragment.ChecksumSHA256 == "":
			unverified = append(unverified, item.ReplicasetUUID)
		case !strings.EqualFold(fragment.ChecksumSHA256, checksum):
			return nil, fmt.Errorf(
				"archive %q of replicaset %s has checksum %s, its fragment says %s: "+
					"the archive changed after it was packed on the node",
				item.LocalPath, item.ReplicasetUUID, checksum, fragment.ChecksumSHA256)
		}

		fragment.ChecksumSHA256 = checksum
	}

	slices.Sort(unverified)

	return unverified, nil
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

	// A plan is produced on one host and consumed on another, so the two tt
	// binaries need not be the same build. Reading a version this one does not
	// know would mean taking the fields it knows and silently dropping
	// whatever the newer plan added -- a chain link, a topology, a scope.
	//
	// The comparison is exact in both directions: an older version is refused
	// as well, because nothing here knows which of its fields this build would
	// misread. A plan lives for the length of one backup, so regenerating it is
	// the cheap answer; the day that stops being true, this becomes a set of
	// accepted versions rather than one constant.
	switch plan.FormatVersion {
	case PlanFormatVersion:
		return &plan, nil
	case 0:
		return nil, fmt.Errorf("plan %q has no format_version: "+
			"it was not written by tt backup plan", filePath)
	default:
		return nil, fmt.Errorf("plan %q has format_version %d, this tt understands %d: "+
			"regenerate the plan with tt backup plan",
			filePath, plan.FormatVersion, PlanFormatVersion)
	}
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
