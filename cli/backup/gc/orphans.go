package gc

import (
	"context"
	"fmt"
	"time"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

// archiveScan is what one pass over data/ tells gc.
type archiveScan struct {
	// orphans are the archives no manifest refers to and that are old enough to
	// delete.
	orphans []Orphan
	// stored is every key the listing showed. A key the manifests name but the
	// storage does not hold is not an archive gc can free, and the same listing
	// answers that without a read per object.
	stored map[string]struct{}
	// notes explain the archives the pass deliberately kept.
	notes []string
}

// scanArchives lists data/ once and works out both halves of what gc needs from
// it: which archives are dangling, and which keys the storage really holds.
func scanArchives(
	ctx context.Context,
	store storage.Storage,
	backupChain *chain.Chain,
	opts Options,
) (archiveScan, error) {
	scan := archiveScan{
		orphans: make([]Orphan, 0),
		stored:  make(map[string]struct{}),
		notes:   make([]string, 0),
	}

	if !opts.hasRetentionRule() {
		// The run is a no-op as a whole; planNotes already said why. Such a run
		// deletes nothing, so it owes the storage no LIST either.
		return scan, nil
	}

	objects, err := store.List(ctx, storage.DataPrefix())
	if err != nil {
		return scan, fmt.Errorf("failed to list archives: %w", err)
	}

	referenced := referencedArchives(backupChain)
	untrusted := untrustedBackups(backupChain)
	newest := newestBackupID(backupChain)
	cutoff := opts.Now.Add(-opts.OrphanAge)
	inFlight, unknown := 0, 0

	for _, object := range objects {
		scan.stored[object.Key] = struct{}{}

		if _, ok := referenced[object.Key]; ok {
			continue
		}

		switch classifyArchive(object, newest, cutoff, untrusted) {
		case archiveInFlight:
			inFlight++
		case archiveUnknown:
			unknown++
		case archiveOrphan:
			scan.orphans = append(scan.orphans, Orphan{
				Key:          object.Key,
				LastModified: object.LastModified,
			})
		case archiveYoung:
		}
	}

	scan.notes = append(scan.notes, orphanNotes(inFlight, unknown, newest == "")...)

	return scan, nil
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

	// An unknown modification time cannot be shown to be old enough.
	young := object.LastModified.IsZero() || !object.LastModified.Before(cutoff)

	// Backup ids sort chronologically, so an archive whose id is above every
	// manifest belongs to an upload whose manifest is not written yet - but only
	// while the archive is still recent. Past --orphan-age that id is just as
	// likely to be the one a killed gc run stripped its manifest from, and
	// shielding it by id alone would leak it for good: no manifest will ever
	// refer to it and no other rule looks at it again.
	switch {
	case young && backup.BackupID(backupID) > newest:
		return archiveInFlight
	case young:
		return archiveYoung
	default:
		return archiveOrphan
	}
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
