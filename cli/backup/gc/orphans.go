package gc

import (
	"context"
	"fmt"
	"time"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

// danglingArchives lists archives that no manifest refers to and that are old
// enough to delete, plus the notes explaining what was skipped.
func danglingArchives(
	ctx context.Context,
	store storage.Storage,
	backupChain *chain.Chain,
	opts Options,
) ([]Orphan, []string, error) {
	orphans := make([]Orphan, 0)
	notes := make([]string, 0)

	if !opts.hasRetentionRule() {
		// The run is a no-op as a whole; planNotes already said why.
		return orphans, notes, nil
	}

	objects, err := store.List(ctx, storage.DataPrefix())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list archives: %w", err)
	}

	referenced := referencedArchives(backupChain)
	untrusted := untrustedBackups(backupChain)
	newest := newestBackupID(backupChain)
	cutoff := opts.Now.Add(-opts.OrphanAge)
	inFlight, unknown := 0, 0

	for _, object := range objects {
		if _, ok := referenced[object.Key]; ok {
			continue
		}

		switch classifyArchive(object, newest, cutoff, untrusted) {
		case archiveInFlight:
			inFlight++
		case archiveUnknown:
			unknown++
		case archiveOrphan:
			orphans = append(orphans, Orphan{
				Key:          object.Key,
				LastModified: object.LastModified,
			})
		case archiveYoung:
		}
	}

	return orphans, append(notes, orphanNotes(inFlight, unknown, newest == "")...), nil
}

// archiveVerdict is why a listed archive is or is not collected.
type archiveVerdict int

const (
	// archiveOrphan is unreferenced, old enough, and safe to delete.
	archiveOrphan archiveVerdict = iota
	// archiveYoung is unreferenced but younger than --orphan-age.
	archiveYoung
	// archiveInFlight belongs to a backup newer than every stored manifest.
	archiveInFlight
	// archiveUnknown does not follow the archive key layout.
	archiveUnknown
)

// classifyArchive decides what to do with one unreferenced archive.
func classifyArchive(
	object storage.ObjectInfo,
	newest backup.BackupID,
	cutoff time.Time,
	untrusted map[string]struct{},
) archiveVerdict {
	backupID, _, ok := storage.ArchiveBackupID(object.Key)
	if !ok {
		// Nothing ties the object to a backup, so the manifest re-check that
		// makes deletion safe is impossible: leave it to a human.
		return archiveUnknown
	}

	if _, broken := untrusted[backupID]; broken {
		// The manifest of this backup is structurally invalid, so its shard list
		// cannot say which archives it still refers to. The archive looks
		// unreferenced only because the manifest cannot be believed - and of all
		// the commands, gc is the one whose mistakes cannot be undone.
		return archiveUnknown
	}

	// A storage without a single manifest is a storage whose first backup may be
	// uploading right now, and there is nothing to compare its archives against.
	if newest == "" {
		return archiveInFlight
	}

	// Backup ids sort chronologically, so an archive newer than every manifest
	// belongs to an upload whose manifest is not written yet. Age cannot express
	// this: a long upload makes its first archives old while it still runs.
	if backup.BackupID(backupID) > newest {
		return archiveInFlight
	}

	// An unknown modification time cannot be shown to be old enough.
	if object.LastModified.IsZero() || !object.LastModified.Before(cutoff) {
		return archiveYoung
	}

	return archiveOrphan
}

// referencedArchives collects every archive key the stored manifests point at.
func referencedArchives(backupChain *chain.Chain) map[string]struct{} {
	referenced := make(map[string]struct{})

	for _, manifest := range backupChain.Manifests() {
		for _, shard := range manifest.Shards {
			if shard.Instance == nil {
				continue
			}

			key, err := storage.CleanKey(shard.Instance.Artifact.Path)
			if err != nil {
				continue
			}

			referenced[key] = struct{}{}
		}
	}

	return referenced
}

// untrustedBackups returns the ids of backups whose own manifest is
// structurally invalid. Only the manifest knows which archives a backup refers
// to, so a manifest that cannot be believed makes its archives unclassifiable.
// Problems inherited from a broken ancestor do not count: such a manifest is
// itself intact and its shard list is as good as any.
func untrustedBackups(backupChain *chain.Chain) map[string]struct{} {
	untrusted := make(map[string]struct{})

	for _, problem := range backupChain.Problems() {
		if problem.Kind == chain.ProblemInvalidManifest && !problem.Inherited {
			untrusted[problem.BackupID] = struct{}{}
		}
	}

	return untrusted
}

// newestBackupID returns the greatest stored backup id, or an empty id when the
// storage holds no manifest at all.
func newestBackupID(backupChain *chain.Chain) backup.BackupID {
	latest := backupChain.Latest()
	if latest == nil {
		return ""
	}

	return latest.Manifest.BackupID
}

// orphanNotes reports the archives that were listed but deliberately kept.
func orphanNotes(inFlight, unknown int, noManifests bool) []string {
	notes := make([]string, 0, 2)

	switch {
	case inFlight > 0 && noManifests:
		notes = append(notes, fmt.Sprintf(
			"%d archive(s) were kept: the storage holds no manifest to tell an "+
				"abandoned archive from the first upload of a new cluster", inFlight,
		))
	case inFlight > 0:
		notes = append(notes, fmt.Sprintf(
			"%d archive(s) newer than the newest manifest were kept: an upload may "+
				"still be in progress", inFlight,
		))
	}

	if unknown > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d archive(s) do not follow the <backup-id>-<replicaset-uuid>.tar.zst "+
				"layout and were kept", unknown,
		))
	}

	return notes
}
