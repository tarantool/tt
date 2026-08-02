package gc

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

// group is one backup chain plus everything gc needs to decide its fate.
type group struct {
	// entries run from the full backup to the newest increment.
	entries []*chain.Entry
	// baseID is the backup id of the full backup this chain is rooted at.
	baseID backup.BackupID
	// rooted reports that the full backup itself is present in the storage.
	rooted bool
	// healthy reports a rooted chain without a single chain problem.
	healthy bool
	// protected marks a chain gc never deletes, and why.
	protected bool
	// protectedAs explains the protection in the run's notes.
	protectedAs string
	// keptByFull marks a chain covered by --keep-full.
	keptByFull bool
}

// Reasons a chain is protected regardless of the retention rules.
const (
	protectedAsHead    = "holds the newest manifest, so an upload may be appending to it"
	protectedAsNewest  = "is the newest chain that can still be recovered from"
	protectedAsOnlyOne = "is the only chain that can still be recovered from"
)

// classifyGroups turns the chain into gc's view of it: which chains are healthy,
// which are protected outright, and which are covered by --keep-full.
//
// Chains are ordered the way the whole storage is ordered — by backup id, which
// starts with a timestamp — so gc agrees with chain.Latest, storage.List and
// every other consumer. Ids that do not sort chronologically are upload's
// problem to reject, not gc's to work around.
func classifyGroups(backupChain *chain.Chain, opts Options) []group {
	// The head chain holds the newest manifest, problems or not: increments are
	// only ever appended to it, so deleting it is the one way to orphan a backup
	// that is being uploaded right now.
	headBase := backup.BackupID("")
	if latest := backupChain.Latest(); latest != nil {
		headBase = latest.Manifest.BaseFullBackupID
	}

	chainGroups := backupChain.Groups()
	groups := make([]group, 0, len(chainGroups))

	for _, chainGroup := range chainGroups {
		if len(chainGroup.Entries) == 0 {
			continue
		}

		current := group{
			entries: chainGroup.Entries,
			baseID:  chainGroup.Entries[0].Manifest.BaseFullBackupID,
		}
		current.rooted = isRooted(chainGroup.Entries, current.baseID)
		current.healthy = current.rooted && !hasProblems(chainGroup.Entries)

		groups = append(groups, current)
	}

	protectHead(groups, headBase, opts)
	protectNewestHealthy(groups)
	markKeptByFull(groups, opts.KeepFull)

	return groups
}

// protectHead shields the chain holding the newest manifest, because an upload
// may be appending an increment to it right now. A chain that lost its full
// backup and whose every manifest is older than --orphan-age is the one
// exception: nothing can be appending to it, and protecting it would make the
// leak the stale-tail rule exists to stop permanent.
func protectHead(groups []group, headBase backup.BackupID, opts Options) {
	for i := range groups {
		if groups[i].baseID != headBase {
			continue
		}

		if !groups[i].rooted && isStale(groups[i], opts) {
			return
		}

		groups[i].protected = true
		groups[i].protectedAs = protectedAsHead

		return
	}
}

// protectNewestHealthy keeps the newest chain that can actually be recovered
// from. Head protection alone is not enough: the newest manifest may belong to a
// chain whose full backup is gone, and protecting only that one would let gc
// delete every usable backup and keep the unusable one.
func protectNewestHealthy(groups []group) {
	newest := -1
	healthy := 0

	for i := range groups {
		if !groups[i].healthy {
			continue
		}

		healthy++
		newest = i
	}

	if newest < 0 || groups[newest].protected {
		return
	}

	groups[newest].protected = true
	groups[newest].protectedAs = protectedAsNewest

	if healthy == 1 {
		groups[newest].protectedAs = protectedAsOnlyOne
	}
}

// isRooted reports whether the full backup of the chain is in the storage.
func isRooted(entries []*chain.Entry, baseID backup.BackupID) bool {
	return slices.ContainsFunc(entries, func(entry *chain.Entry) bool {
		return entry.Manifest.BackupID == baseID
	})
}

// hasProblems reports whether any manifest of the chain carries a chain problem.
func hasProblems(entries []*chain.Entry) bool {
	return slices.ContainsFunc(entries, func(entry *chain.Entry) bool {
		return len(entry.Problems) > 0
	})
}

// markKeptByFull keeps the last N healthy chains. A chain with problems is not
// counted: it is not a usable recovery chain, and counting it would let a broken
// tail push a healthy chain out of the retention window.
func markKeptByFull(groups []group, keepFull int) {
	if keepFull <= 0 {
		return
	}

	kept := 0
	for i := len(groups) - 1; i >= 0 && kept < keepFull; i-- {
		if !groups[i].healthy {
			continue
		}

		groups[i].keptByFull = true
		kept++
	}
}

// deletableBackups returns the backups of one chain that this run may delete,
// in deletion order: newest first, so that an interrupted run leaves a valid
// chain prefix behind rather than an orphaned tail.
func deletableBackups(current group, opts Options) []Backup {
	if !opts.hasRetentionRule() || current.protected || current.keptByFull {
		return nil
	}

	if !current.healthy {
		return staleTailBackups(current, opts)
	}

	// The chain is cut from the tail only: a backup may go only once every
	// increment that depends on it goes too, otherwise the kept increment is
	// left unrecoverable.
	cut := len(current.entries)
	for cut > 0 && !opts.keepsByAge(current.entries[cut-1].Manifest) {
		cut--
	}

	return backupsOf(current.entries[cut:])
}

// staleTailBackups deletes a chain that lost its full backup once every manifest
// in it is older than --orphan-age. Such a tail can never be recovered from, and
// nothing else would ever collect it.
func staleTailBackups(current group, opts Options) []Backup {
	if current.rooted {
		// A rooted chain with problems (a fork, say) is diagnostic material:
		// gc does not fix or delete it, tt backup verify reports it.
		return nil
	}

	if !isStale(current, opts) {
		return nil
	}

	return backupsOf(current.entries)
}

// isStale reports whether every manifest of a chain is older than --orphan-age.
func isStale(current group, opts Options) bool {
	cutoff := opts.Now.Add(-opts.OrphanAge)
	for _, entry := range current.entries {
		// A manifest without a creation time cannot be shown to be old enough;
		// treating the zero time as "ancient" would delete a fresh upload.
		created := entry.Manifest.CreationTime
		if created.IsZero() || !created.Before(cutoff) {
			return false
		}
	}

	return true
}

// backupsOf turns chain entries into deletions, newest backup first.
func backupsOf(entries []*chain.Entry) []Backup {
	backups := make([]Backup, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		backups = append(backups, backupOf(entries[i].Manifest))
	}

	return backups
}

// backupOf collects the storage keys one manifest owns.
func backupOf(manifest *backup.ClusterManifest) Backup {
	archives := make([]string, 0, len(manifest.Shards))
	for _, shard := range manifest.Shards {
		if shard.Instance == nil {
			continue
		}

		// A path that is not a storage key was never written by upload; leave it
		// to verify to report rather than guessing what to delete.
		key, err := storage.CleanKey(shard.Instance.Artifact.Path)
		if err != nil {
			continue
		}

		archives = append(archives, key)
	}

	slices.Sort(archives)

	return Backup{
		BackupID:    string(manifest.BackupID),
		ManifestKey: storage.ManifestKey(string(manifest.BackupID)),
		ArchiveKeys: archives,
	}
}

// keepKeysUnderTheDataPrefix drops from the plan every archive key that does not
// live under data/. artifact.path is manifest content and nothing validates it,
// so a hand-edited or third-party manifest can name any object in the storage -
// another chain's manifest included - and gc would delete it as if it were an
// archive it had uploaded itself.
func keepKeysUnderTheDataPrefix(backups []Backup) ([]Backup, []string) {
	return keepArchiveKeys(backups, func(deleted Backup, key string) string {
		if strings.HasPrefix(key, storage.DataPrefix()) {
			return ""
		}

		return fmt.Sprintf(
			"backup %q names %q, which is not an archive under %s: the "+
				"manifest is malformed and the path was left alone",
			deleted.BackupID, key, storage.DataPrefix(),
		)
	})
}

// keepKeysTheStorageHolds drops from the plan every archive key the storage does
// not hold. Such a key frees nothing - a manifest may name an object no upload
// ever wrote, or one an earlier run removed - while Delete answers OK for it all
// the same, so leaving it in would make the run count a deletion that did not
// happen and promise an operator bytes it cannot free.
func keepKeysTheStorageHolds(backups []Backup, stored map[string]struct{}) ([]Backup, []string) {
	return keepArchiveKeys(backups, func(deleted Backup, key string) string {
		if _, ok := stored[key]; ok {
			return ""
		}

		return fmt.Sprintf(
			"backup %q names %q, which the storage does not hold: nothing was "+
				"deleted for it and nothing is counted as freed",
			deleted.BackupID, key,
		)
	})
}

// keepArchiveKeys drops from every backup of the plan the archive keys rejected
// gives a reason for, and returns those reasons as the run's notes. What makes a
// key undeletable differs from rule to rule; the bookkeeping does not.
func keepArchiveKeys(
	backups []Backup,
	rejected func(deleted Backup, key string) string,
) ([]Backup, []string) {
	kept := make([]Backup, 0, len(backups))
	notes := make([]string, 0)

	for _, deleted := range backups {
		archives := make([]string, 0, len(deleted.ArchiveKeys))

		for _, key := range deleted.ArchiveKeys {
			if note := rejected(deleted, key); note != "" {
				notes = append(notes, note)

				continue
			}

			archives = append(archives, key)
		}

		deleted.ArchiveKeys = archives
		kept = append(kept, deleted)
	}

	return kept, notes
}

// cannotEmptyNote is the sentence every run owes the user: no combination of
// flags empties a storage, so nobody waits for a cleanup that will never happen.
const cannotEmptyNote = "gc never deletes every chain: to wipe a storage, remove " +
	"the storage root with your storage tooling instead"

// planNotes explains what the run deliberately left alone.
func planNotes(groups []group, opts Options) []string {
	notes := make([]string, 0, len(groups)+1)

	if !opts.hasRetentionRule() {
		return append(notes,
			"no retention rule given: pass --keep-full and/or --keep-days "+
				"for gc to delete anything",
			cannotEmptyNote,
		)
	}

	for _, current := range groups {
		switch {
		case current.protected:
			notes = append(notes, fmt.Sprintf(
				"chain %q is kept whatever the rules say: it %s",
				current.baseID, current.protectedAs,
			))
		case current.rooted && !current.healthy:
			notes = append(notes, fmt.Sprintf(
				"chain %q has chain problems and was left alone; run tt backup verify",
				current.baseID,
			))
		}
	}

	return append(notes, cannotEmptyNote)
}
