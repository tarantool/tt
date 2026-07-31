// Package verify health-checks a backup storage: manifest chain integrity,
// archive presence and checksums, dangling archives. It never deletes anything.
package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

// IssueKind classifies one problem found in the storage.
type IssueKind string

const (
	// IssueMissingArchive marks a manifest referencing an archive that is not stored.
	IssueMissingArchive IssueKind = "missing_archive"
	// IssueUnreadableArchive marks an archive that exists but cannot be read to the end.
	IssueUnreadableArchive IssueKind = "unreadable_archive"
	// IssueChecksumMismatch marks an archive whose bytes do not match the manifest.
	IssueChecksumMismatch IssueKind = "checksum_mismatch"
	// IssueChecksumMissing marks a manifest that carries no checksum to compare against.
	IssueChecksumMissing IssueKind = "checksum_missing"
	// IssueDanglingArchive marks a stored archive no manifest refers to.
	IssueDanglingArchive IssueKind = "dangling_archive"
	// IssueChainOrphan marks a manifest whose previous backup is absent.
	IssueChainOrphan IssueKind = "chain_orphan"
	// IssueChainFork marks a manifest sharing its previous backup with another one.
	IssueChainFork IssueKind = "chain_fork"
	// IssueVclockMismatch marks an increment that does not continue its ancestor.
	IssueVclockMismatch IssueKind = "vclock_mismatch"
	// IssueInvalidManifest marks a structurally invalid manifest.
	IssueInvalidManifest IssueKind = "invalid_manifest"
	// IssueUnreadableManifest marks a stored manifest that could not be read or decoded.
	IssueUnreadableManifest IssueKind = "unreadable_manifest"
	// IssueChainProblem marks a chain problem this package does not classify further.
	IssueChainProblem IssueKind = "chain_problem"
)

// Issue is one problem found in the storage.
type Issue struct {
	// Kind classifies the problem.
	Kind IssueKind `json:"kind"`
	// BackupID identifies the affected manifest, empty for a dangling archive.
	BackupID string `json:"backup_id,omitempty"`
	// Manifest is the storage key of the affected manifest. It is the only name
	// left when the content is unusable: a manifest carrying no backup_id, or one
	// of two objects claiming the same one, cannot be pointed at any other way.
	Manifest string `json:"manifest,omitempty"`
	// ReplicasetUUID identifies the affected shard, when the problem is shard-local.
	ReplicasetUUID string `json:"replicaset_uuid,omitempty"`
	// Archive is the storage key of the affected archive, when there is one.
	Archive string `json:"archive,omitempty"`
	// Inherited reports a chain problem that originated in an ancestor manifest.
	Inherited bool `json:"inherited,omitempty"`
	// Detail explains the problem in one line.
	Detail string `json:"detail"`
}

// Report is the outcome of a storage health check.
type Report struct {
	// Manifests is the number of manifests read from the storage.
	Manifests int `json:"manifests_checked"`
	// Archives is the number of archives referenced by those manifests.
	Archives int `json:"archives_checked"`
	// Issues lists every problem found, manifest by manifest in chain order,
	// dangling archives last.
	Issues []Issue `json:"issues"`
}

// OK reports whether the storage is healthy.
func (r *Report) OK() bool {
	return len(r.Issues) == 0
}

// Verify reads every manifest, recomputes the checksum of every archive it
// references, and lists archives no manifest refers to. It only reads: no
// object is written or deleted, whatever it finds.
func Verify(ctx context.Context, store storage.Storage) (*Report, error) {
	// A manifest that cannot be read is a finding, not a reason to stop: the
	// rest of the storage still has to be checked.
	backupChain, unreadable, err := chain.LoadPartial(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("failed to load backup chain: %w", err)
	}

	// A manifest that was not read because the run was cut short says nothing
	// about the storage. Calling it unreadable would let an expired deadline
	// masquerade as corruption.
	for _, manifest := range unreadable {
		if isRunCutShort(manifest.Err) {
			return nil, fmt.Errorf("failed to read manifest %q: %w", manifest.Key, manifest.Err)
		}
	}

	// An unreadable manifest still counts as seen, and it is reported first: the
	// problems below may well be its missing links.
	report := &Report{
		Manifests: len(unreadable),
		Issues:    unreadableIssues(unreadable),
	}
	// referenced collects archive keys claimed by a manifest, so that whatever
	// is left in data/ afterwards is dangling.
	referenced := make(map[string]struct{})
	// An unreadable manifest still owns its archives; it just cannot be parsed
	// to say which. Calling them dangling would tell the operator - and gc, which
	// consumes exactly this issue - to delete live backup data, so the backups
	// behind those manifests are excluded from the dangling scan instead.
	undetermined := undeterminedBackups(unreadable)

	for _, group := range backupChain.Groups() {
		for _, entry := range group.Entries {
			report.Manifests++
			report.Issues = append(report.Issues, chainIssues(entry)...)

			archives, issues, err := verifyArchives(ctx, store, entry.Manifest, referenced)
			if err != nil {
				return nil, fmt.Errorf("failed to check backup %q: %w",
					entry.Manifest.BackupID, err)
			}

			report.Archives += archives
			report.Issues = append(report.Issues, issues...)
		}
	}

	dangling, err := danglingIssues(ctx, store, referenced, undetermined)
	if err != nil {
		return nil, fmt.Errorf("failed to check for dangling archives: %w", err)
	}

	report.Issues = append(report.Issues, dangling...)

	return report, nil
}

// unreadableIssues converts the manifests the chain could not read into issues.
func unreadableIssues(unreadable []chain.Unreadable) []Issue {
	issues := make([]Issue, 0, len(unreadable))

	for _, manifest := range unreadable {
		// The content is unusable, so the key is all that names the backup. It is
		// carried explicitly rather than left to the error text: an operator has
		// to be able to find the object, and a key derived from the id is exactly
		// what is unavailable here.
		backupID, _ := storage.ManifestBackupID(manifest.Key)
		issues = append(issues, Issue{
			Kind:     IssueUnreadableManifest,
			BackupID: backupID,
			Manifest: manifest.Key,
			Detail:   fmt.Sprintf("manifest is unusable: %v", manifest.Err),
		})
	}

	return issues
}

// chainIssues converts the chain problems of one manifest into issues.
func chainIssues(entry *chain.Entry) []Issue {
	issues := make([]Issue, 0, len(entry.Problems))

	for _, problem := range entry.Problems {
		issues = append(issues, Issue{
			Kind:      chainIssueKind(problem.Kind),
			BackupID:  problem.BackupID,
			Inherited: problem.Inherited,
			Detail:    problem.Detail,
		})
	}

	return issues
}

// chainIssueKind maps a chain problem kind to an issue kind.
func chainIssueKind(kind chain.ProblemKind) IssueKind {
	switch kind {
	case chain.ProblemOrphan:
		return IssueChainOrphan
	case chain.ProblemFork:
		return IssueChainFork
	case chain.ProblemVclockMismatch:
		return IssueVclockMismatch
	case chain.ProblemInvalidManifest:
		return IssueInvalidManifest
	default:
		return IssueChainProblem
	}
}

// verifyArchives checks every archive one manifest refers to, adding their keys
// to referenced, and returns the number of archives checked.
func verifyArchives(
	ctx context.Context,
	store storage.Storage,
	manifest *backup.ClusterManifest,
	referenced map[string]struct{},
) (int, []Issue, error) {
	// Sort the shards so that the report of one manifest is stable across runs.
	replicasetUUIDs := make([]string, 0, len(manifest.Shards))
	for replicasetUUID := range manifest.Shards {
		replicasetUUIDs = append(replicasetUUIDs, replicasetUUID)
	}

	slices.Sort(replicasetUUIDs)

	archives := 0
	issues := make([]Issue, 0)

	for _, replicasetUUID := range replicasetUUIDs {
		// A shard that failed to back up carries an error instead of an artifact;
		// the manifest already records it, there is nothing to check in storage.
		instance := manifest.Shards[replicasetUUID].Instance
		if instance == nil {
			continue
		}

		archives++

		archiveIssues, err := verifyArchive(
			ctx, store, manifest, replicasetUUID, instance, referenced,
		)
		if err != nil {
			return archives, issues, fmt.Errorf("failed to check shard %q: %w",
				replicasetUUID, err)
		}

		issues = append(issues, archiveIssues...)
	}

	return archives, issues, nil
}

// verifyArchive checks presence and checksum of one shard archive.
func verifyArchive(
	ctx context.Context,
	store storage.Storage,
	manifest *backup.ClusterManifest,
	replicasetUUID string,
	instance *backup.ShardInstance,
	referenced map[string]struct{},
) ([]Issue, error) {
	issue := func(kind IssueKind, key, format string, args ...any) []Issue {
		return []Issue{{
			Kind:           kind,
			BackupID:       string(manifest.BackupID),
			ReplicasetUUID: replicasetUUID,
			Archive:        key,
			Detail:         fmt.Sprintf(format, args...),
		}}
	}

	key, err := storage.CleanKey(instance.Artifact.Path)
	if err != nil {
		return issue(IssueMissingArchive, instance.Artifact.Path,
			"manifest artifact path %q is not a storage key: %v",
			instance.Artifact.Path, err), nil
	}

	referenced[key] = struct{}{}

	checksum, err := archiveChecksum(ctx, store, key)
	switch {
	case errors.Is(err, storage.ErrKeyNotFound):
		return issue(IssueMissingArchive, key, "archive is not present in the storage"), nil
	case isRunCutShort(err):
		// The archive was never read, so nothing is known about it. Reporting it
		// as unreadable would turn "I ran out of time" into "your backups are
		// corrupt" - and, with the archives after it unchecked too, into a pile
		// of dangling reports as well.
		return nil, fmt.Errorf("failed to read archive %q: %w", key, err)
	case err != nil:
		return issue(IssueUnreadableArchive, key, "failed to read archive: %v", err), nil
	}

	expected := instance.Artifact.ChecksumSHA256
	switch {
	case expected == "":
		return issue(IssueChecksumMissing, key,
			"manifest has no checksum_sha256, actual checksum is %s", checksum), nil
	case !strings.EqualFold(expected, checksum):
		return issue(IssueChecksumMismatch, key,
			"checksum_sha256 is %s, actual checksum is %s", expected, checksum), nil
	}

	return nil, nil
}

// isRunCutShort reports whether an error means the run was stopped rather than
// the storage being at fault: a cancelled context or an expired --timeout. Such
// a run must exit "could not check", never "problems found".
func isRunCutShort(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// archiveChecksum streams the archive out of the storage into a hash: archives
// are hundreds of megabytes, and verify has no reason to keep or spool them.
func archiveChecksum(ctx context.Context, store storage.Storage, key string) (string, error) {
	reader, err := store.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to get archive %q: %w", key, err)
	}
	defer reader.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, reader); err != nil {
		return "", fmt.Errorf("failed to read archive %q: %w", key, err)
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
}

// undeterminedBackups returns the ids of backups whose manifest could not be
// read. Their archives cannot be classified: only the manifest knows whether it
// still refers to them. A key that does not name a manifest at all claims no
// archives, so it is skipped.
func undeterminedBackups(unreadable []chain.Unreadable) map[string]struct{} {
	backups := make(map[string]struct{}, len(unreadable))

	for _, manifest := range unreadable {
		if backupID, ok := storage.ManifestBackupID(manifest.Key); ok {
			backups[backupID] = struct{}{}
		}
	}

	return backups
}

// danglingIssues lists stored archives that no manifest refers to. Archives of a
// backup in undetermined are left out: their manifest is unreadable, so whether
// they are still referenced is unknown, and unknown must not read as garbage.
func danglingIssues(
	ctx context.Context,
	store storage.Storage,
	referenced map[string]struct{},
	undetermined map[string]struct{},
) ([]Issue, error) {
	objects, err := store.List(ctx, storage.DataPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to list archives: %w", err)
	}

	issues := make([]Issue, 0)
	for _, object := range objects {
		if _, ok := referenced[object.Key]; ok {
			continue
		}

		if backupID, _, ok := storage.ArchiveBackupID(object.Key); ok {
			if _, undecided := undetermined[backupID]; undecided {
				continue
			}
		}

		issues = append(issues, Issue{
			Kind:    IssueDanglingArchive,
			Archive: object.Key,
			Detail: fmt.Sprintf(
				"archive is not referenced by any manifest (%d bytes, modified %s)",
				object.Size,
				object.LastModified.UTC().Format("2006-01-02T15:04:05Z"),
			),
		})
	}

	return issues, nil
}
