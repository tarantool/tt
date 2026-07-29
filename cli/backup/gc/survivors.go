package gc

import (
	"fmt"
	"slices"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

// keepWhatSurvivorsNeed drops from the plan everything a manifest that stays
// behind still depends on: the backups it is recovered through, and the archives
// it points at. The retention rules already reason chain by chain, but a manifest
// is free to name a previous backup in another chain or an archive another backup
// uploaded, and neither is something chain.Build treats as a problem. gc cannot
// verify such a storage, only avoid making it worse.
func keepWhatSurvivorsNeed(
	backups []Backup,
	backupChain *chain.Chain,
) ([]Backup, []string) {
	if len(backups) == 0 {
		return backups, nil
	}

	doomed := make(map[string]struct{}, len(backups))
	for _, deleted := range backups {
		doomed[deleted.BackupID] = struct{}{}
	}

	manifests, survivors := splitByFate(backupChain, doomed)
	needed := ancestorsOfSurvivors(survivors, manifests, doomed)
	referenced := referencedBy(survivors, needed, manifests)

	kept := make([]Backup, 0, len(backups))
	notes := make([]string, 0)
	// planned holds the archives already scheduled by an earlier backup of this
	// run: two doomed manifests may name one archive, and deleting it twice would
	// overstate what the run removed.
	planned := make(map[string]struct{})

	for _, deleted := range backups {
		if _, ok := needed[deleted.BackupID]; ok {
			notes = append(notes, fmt.Sprintf(
				"backup %q is kept: a surviving manifest is recovered through it",
				deleted.BackupID,
			))

			continue
		}

		archives, shared := splitSharedArchives(deleted.ArchiveKeys, referenced, planned)
		for _, key := range shared {
			notes = append(notes, fmt.Sprintf(
				"archive %q is kept: a surviving manifest still refers to it", key,
			))
		}

		for _, key := range archives {
			planned[key] = struct{}{}
		}

		deleted.ArchiveKeys = archives
		kept = append(kept, deleted)
	}

	return kept, notes
}

// splitByFate indexes every stored manifest and collects the ones the plan
// leaves in the storage.
func splitByFate(
	backupChain *chain.Chain,
	doomed map[string]struct{},
) (map[string]*backup.ClusterManifest, []*backup.ClusterManifest) {
	stored := backupChain.Manifests()
	manifests := make(map[string]*backup.ClusterManifest, len(stored))
	survivors := make([]*backup.ClusterManifest, 0, len(stored))

	for _, manifest := range stored {
		id := string(manifest.BackupID)
		manifests[id] = manifest

		if _, ok := doomed[id]; !ok {
			survivors = append(survivors, manifest)
		}
	}

	return manifests, survivors
}

// ancestorsOfSurvivors walks previous_backup_id back from every surviving
// manifest and returns the doomed backups found on the way: recovery replays
// them, so they have to outlive the run.
func ancestorsOfSurvivors(
	survivors []*backup.ClusterManifest,
	manifests map[string]*backup.ClusterManifest,
	doomed map[string]struct{},
) map[string]struct{} {
	needed := make(map[string]struct{})

	for _, survivor := range survivors {
		id := string(survivor.PreviousBackupID)

		for id != "" {
			if _, ok := doomed[id]; !ok {
				// The ancestor stays anyway; its own ancestors are covered when it
				// is walked as a survivor itself.
				break
			}

			if _, seen := needed[id]; seen {
				// Already walked, and the guard also ends a previous_backup_id cycle.
				break
			}

			needed[id] = struct{}{}

			parent, ok := manifests[id]
			if !ok {
				break
			}

			id = string(parent.PreviousBackupID)
		}
	}

	return needed
}

// referencedBy collects the archive keys the manifests that stay behind point at.
func referencedBy(
	survivors []*backup.ClusterManifest,
	needed map[string]struct{},
	manifests map[string]*backup.ClusterManifest,
) map[string]struct{} {
	referenced := make(map[string]struct{})

	collect := func(manifest *backup.ClusterManifest) {
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

	for _, survivor := range survivors {
		collect(survivor)
	}

	for id := range needed {
		if manifest, ok := manifests[id]; ok {
			collect(manifest)
		}
	}

	return referenced
}

// splitSharedArchives separates the archives only this deleted backup owns from
// the ones a surviving manifest also points at. Archives another doomed backup
// already scheduled are dropped silently: they are deleted either way.
func splitSharedArchives(
	keys []string,
	referenced, planned map[string]struct{},
) ([]string, []string) {
	owned := make([]string, 0, len(keys))
	shared := make([]string, 0)

	for _, key := range keys {
		if _, ok := referenced[key]; ok {
			shared = append(shared, key)
			continue
		}

		if _, ok := planned[key]; ok {
			continue
		}

		owned = append(owned, key)
	}

	slices.Sort(shared)

	return owned, shared
}
