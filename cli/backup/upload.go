package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
		// The manifest key goes into the rollback too. A PUT that reached the
		// storage and then failed the client -- a timeout on the response -- is
		// invisible from here, and it would leave a manifest naming archives
		// this rollback is about to delete. Worse, that manifest becomes the
		// chain head, so the retry of this very backup is refused for reusing
		// an id, and the next one is refused for not continuing it.
		deleteObjects(ctx, store, append(uploadedKeys, manifestKey))

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

// CheckChainHead compares the storage against the plan the backup was taken
// from. head is the newest manifest in the storage, nil when there is none.
//
// Two things can have happened between planning and uploading, and both are
// silent: another upload landed -- so the increment about to be stored does not
// continue the backup it was planned against -- or the id of this backup does
// not sort above the stored one, and the manifest would land in the middle of a
// chain that is read by id. Neither is recoverable afterwards, so both are
// refused before anything is written.
//
// Ids are compared as strings, which is how every reader of a storage orders
// them -- LatestManifest takes the greatest key, the chain sorts by backup id,
// gc walks that order. An id scheme that does not sort in the order the backups
// are taken (a bare counter: backup-2, backup-10) is therefore refused here
// rather than quietly producing a storage whose newest backup is not the last
// one taken.
func CheckChainHead(head *ClusterManifest, plan *BackupPlan, backupID BackupID) error {
	previous := BackupID(plan.PreviousBackupID)

	if head == nil {
		if previous != "" {
			return fmt.Errorf(
				"the plan continues backup %q, but the storage holds no backup at all",
				previous)
		}

		return nil
	}

	// Ids order the chain, so an id that does not sort above the head would be
	// read as older than the backup it was taken after.
	if backupID <= head.BackupID {
		return fmt.Errorf(
			"backup id %q does not sort above the newest stored backup %q: "+
				"ids are compared as text and order the chain, so either the clock "+
				"of this host is behind, the id is already used, or the ids are not "+
				"generated in an order that sorts (a zero-padded timestamp does)",
			backupID, head.BackupID)
	}

	if plan.Type == BackupTypeIncremental && previous != head.BackupID {
		return fmt.Errorf(
			"the plan continues backup %q, but the newest stored backup is %q: "+
				"the storage changed since the plan was made, so this increment "+
				"would not continue what it was planned against",
			previous, head.BackupID)
	}

	return nil
}

// PromotionWarning reports why a full backup is landing on top of an existing
// chain, or nil when it is not landing on one or nothing changed.
//
// The reason is derived rather than declared: an orchestrator asking for a full
// backup has no way to tell tt why, and the two manifests already say it. A
// scheduled full backup of an unchanged cluster produces no warning.
func PromotionWarning(head *ClusterManifest, plan *BackupPlan) *Warning {
	if head == nil || plan.Type != BackupTypeFull {
		return nil
	}

	planned := slices.Sorted(maps.Keys(plan.Replicasets))
	backedUp := slices.Sorted(maps.Keys(head.Shards))

	if !slices.Equal(planned, backedUp) {
		warning := NewPromotedToFullWarning(PromotedTopologyChanged, fmt.Sprintf(
			"the cluster holds replicasets %s, backup %q holds %s",
			strings.Join(planned, ", "), head.BackupID, strings.Join(backedUp, ", ")))

		return &warning
	}

	for _, replicasetUUID := range planned {
		shard := head.Shards[replicasetUUID]
		if shard.Instance == nil {
			continue
		}

		master := plan.Replicasets[replicasetUUID].MasterInstanceUUID
		if master == shard.Instance.InstanceUUID {
			continue
		}

		warning := NewPromotedToFullWarning(PromotedMasterChanged, fmt.Sprintf(
			"replicaset %s is backed up on instance %s now, backup %q was taken on %s",
			replicasetUUID, master, head.BackupID, shard.Instance.InstanceUUID))

		return &warning
	}

	return nil
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

// ValidateFragmentsAgainstPlan checks that the fragments are the ones the plan
// asked for: every expected replicaset produced one, each was taken on the
// instance the plan named as that replicaset's master, and each is of the
// backup type the plan asked for.
//
// The master check is what catches a failover between `plan` and `start`: the
// backup is then taken on another instance, and an increment of another
// instance's journal does not continue the chain it is being appended to. tt
// notices a master change when it plans, but the window after that is exactly
// where nothing was looking.
//
// The comparisons are only as good as the plan: what tt derived from the
// cluster is authoritative, what an operator wrote by hand is their word. So a
// disagreement with a plan tt produced is refused, and a disagreement with a
// hand-written or edited one is returned as a note for the caller to report.
// Which of the two it is comes from the plan's own checksum (PlanIsAuthentic),
// and is the caller's answer to pass, since only it knows how the plan arrived.
//
// Returns the notes about checks that were made but not enforced, and about
// checks the plan states too little to make at all.
func ValidateFragmentsAgainstPlan(
	fragments []*Fragment,
	plan *BackupPlan,
	authentic bool,
) ([]string, error) {
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

		return nil, fmt.Errorf("plan expects %d replicasets but fragments are missing for: %s",
			len(plan.Replicasets), strings.Join(missing, ", "))
	}

	unchecked := make([]string, 0)

	for _, fragment := range fragments {
		replicasetPlan, planned := plan.Replicasets[fragment.ReplicasetUUID]
		if !planned {
			// A shard the plan does not expect: the cluster gained a
			// replicaset since it was made, or the orchestrator collected a
			// fragment from another backup.
			note := fmt.Sprintf(
				"fragment of replicaset %s is not in the plan, which expects %s",
				fragment.ReplicasetUUID,
				strings.Join(slices.Sorted(maps.Keys(plan.Replicasets)), ", "))

			if authentic {
				return nil, errors.New(note)
			}

			unchecked = append(unchecked, note+" (the plan was not written by tt backup plan)")

			continue
		}

		notes, err := checkFragmentAgainstPlan(fragment, replicasetPlan, plan.Type, authentic)
		if err != nil {
			return nil, err
		}

		unchecked = append(unchecked, notes...)
	}

	slices.Sort(unchecked)

	return unchecked, nil
}

// checkFragmentAgainstPlan compares one fragment with what the plan says about
// its replicaset. It returns what could not be checked, or was checked and
// disagreed with a plan tt did not write.
func checkFragmentAgainstPlan(
	fragment *Fragment,
	replicasetPlan ReplicasetPlan,
	mode BackupType,
	authentic bool,
) ([]string, error) {
	notes := make([]string, 0, 2)

	// A disagreement is a refusal only when the plan is tt's own: then it
	// describes the cluster as tt found it, and a fragment that contradicts it
	// was taken somewhere else. A hand-written plan describes what the operator
	// believes, and tt is in no position to overrule it.
	report := func(note string) error {
		if authentic {
			return errors.New(note)
		}

		notes = append(notes, note+" (the plan was not written by tt backup plan)")

		return nil
	}

	switch {
	case mode == "":
		notes = append(notes, fmt.Sprintf(
			"the plan states no backup mode, so the type of the fragment of "+
				"replicaset %s is taken on trust", fragment.ReplicasetUUID))
	case fragment.Type != mode:
		if err := report(fmt.Sprintf(
			"fragment of replicaset %s is a %s backup, the plan asks for %s",
			fragment.ReplicasetUUID, fragment.Type, mode)); err != nil {
			return nil, err
		}
	}

	switch {
	case replicasetPlan.MasterInstanceUUID == "":
		notes = append(notes, fmt.Sprintf(
			"the plan names no master for replicaset %s, so the instance its "+
				"backup was taken on is taken on trust", fragment.ReplicasetUUID))
	case fragment.InstanceUUID != replicasetPlan.MasterInstanceUUID:
		if err := report(fmt.Sprintf(
			"fragment of replicaset %s was taken on instance %s, the plan names %s "+
				"as its master: a backup taken elsewhere -- after a failover, or on "+
				"the wrong node -- does not continue the chain this plan was made for",
			fragment.ReplicasetUUID, fragment.InstanceUUID,
			replicasetPlan.MasterInstanceUUID)); err != nil {
			return nil, err
		}
	}

	return notes, nil
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
