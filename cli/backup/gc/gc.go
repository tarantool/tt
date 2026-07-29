// Package gc deletes backup chains by retention rules and cleans up dangling
// archives. It is the only destructive operation on a backup storage, so every
// run first builds a plan that can be inspected (tt backup gc --dry-run) and
// only then executes it.
package gc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

// DefaultOrphanAge is how old a dangling archive must be before gc removes it.
// It has to outlive a full upload: archives are uploaded before the manifest
// that references them, so a shorter window would delete the archives of a
// backup that is still being uploaded.
//
//nolint:goconst // equal to the tests' `day` unit by coincidence, not by meaning
const DefaultOrphanAge = 24 * time.Hour

// Options are the retention rules of one gc run.
type Options struct {
	// KeepFull keeps the last N healthy backup chains; 0 means the rule is unset.
	KeepFull int
	// KeepDays keeps backups created within the last N days; 0 means unset.
	KeepDays int
	// OrphanAge is the minimum age of a dangling archive eligible for deletion.
	OrphanAge time.Duration
	// Now is the reference time for age rules; zero means time.Now().
	Now time.Time
}

// Backup is one backup scheduled for deletion.
type Backup struct {
	// BackupID identifies the backup.
	BackupID string `json:"backup_id"`
	// ManifestKey is deleted first: a manifest without archives breaks recovery
	// loudly, archives without a manifest are collected by the next run.
	ManifestKey string `json:"manifest_key"`
	// ArchiveKeys are the archives the manifest referred to, sorted by key.
	ArchiveKeys []string `json:"archive_keys"`
}

// Orphan is a dangling archive scheduled for deletion.
type Orphan struct {
	// Key is the storage key of the archive.
	Key string `json:"key"`
	// LastModified is when the storage last wrote the archive.
	LastModified time.Time `json:"last_modified"`
}

// Plan is everything one gc run would delete, in deletion order.
type Plan struct {
	// Backups are ordered group by group, newest backup of a group first.
	Backups []Backup `json:"backups"`
	// Orphans are dangling archives, sorted by key.
	Orphans []Orphan `json:"orphans"`
	// Notes explain what the run deliberately left alone.
	Notes []string `json:"notes"`
}

// Empty reports whether the plan deletes nothing.
func (p *Plan) Empty() bool {
	return len(p.Backups) == 0 && len(p.Orphans) == 0
}

// Archives counts the archives of every backup in the plan.
func (p *Plan) Archives() int {
	archives := 0
	for _, deleted := range p.Backups {
		archives += len(deleted.ArchiveKeys)
	}

	return archives
}

// Result is what a gc run actually deleted.
type Result struct {
	// Backups is the number of backups whose manifest was deleted.
	Backups int `json:"backups_deleted"`
	// Archives is the number of archives deleted together with their backup.
	Archives int `json:"archives_deleted"`
	// Orphans is the number of dangling archives deleted.
	Orphans int `json:"orphans_deleted"`
	// Kept lists dangling archives that were skipped after a second look.
	Kept []string `json:"orphans_kept"`
}

// BuildPlan works out what a gc run with these options would delete. It only
// reads the storage.
func BuildPlan(
	ctx context.Context,
	store storage.Storage,
	opts Options,
) (*Plan, error) {
	// gc must not act on a partial picture of the chain: an unreadable manifest
	// could be the parent that makes a whole group non-deletable.
	backupChain, err := chain.Load(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("failed to load backup chain: %w", err)
	}

	opts = opts.withDefaults()
	plan := &Plan{
		Backups: make([]Backup, 0),
		Orphans: make([]Orphan, 0),
		Notes:   make([]string, 0),
	}

	groups := classifyGroups(backupChain, opts)
	for _, group := range groups {
		plan.Backups = append(plan.Backups, deletableBackups(group, opts)...)
	}

	plan.Notes = append(plan.Notes, planNotes(groups, opts)...)

	// Last safety net before anything is deleted: whatever the rules worked
	// out per chain, no surviving manifest may end up pointing at a deleted
	// object. Manifests that link across chains are not something chain.Build
	// flags, and gc is the one command that cannot afford to be wrong about it.
	backups, keptNotes := keepWhatSurvivorsNeed(plan.Backups, backupChain)
	plan.Backups = backups
	plan.Notes = append(plan.Notes, keptNotes...)

	orphans, notes, err := danglingArchives(ctx, store, backupChain, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to collect dangling archives: %w", err)
	}

	plan.Orphans = orphans
	plan.Notes = append(plan.Notes, notes...)

	return plan, nil
}

// Execute deletes what the plan describes. Backups go first and, inside one
// backup, the manifest goes before its archives, so that an interrupted run
// leaves collectable garbage instead of a manifest pointing at nothing.
func Execute(
	ctx context.Context,
	store storage.Storage,
	plan *Plan,
) (*Result, error) {
	result := &Result{Kept: make([]string, 0)}

	for _, deleted := range plan.Backups {
		if err := store.Delete(ctx, deleted.ManifestKey); err != nil {
			return result, fmt.Errorf(
				"failed to delete manifest %q: %w", deleted.ManifestKey, err,
			)
		}

		result.Backups++

		for _, key := range deleted.ArchiveKeys {
			if err := store.Delete(ctx, key); err != nil {
				return result, fmt.Errorf("failed to delete archive %q: %w", key, err)
			}

			result.Archives++
		}
	}

	for _, orphan := range plan.Orphans {
		kept, err := deleteOrphan(ctx, store, orphan)
		if err != nil {
			return result, fmt.Errorf("failed to collect dangling archive: %w", err)
		}

		if kept {
			result.Kept = append(result.Kept, orphan.Key)
			continue
		}

		result.Orphans++
	}

	return result, nil
}

// deleteOrphan removes one dangling archive after making sure its manifest
// really is absent. LIST is not read-after-write consistent (an NFS mount, a
// mediator index), so a listing that missed a just-uploaded manifest would make
// gc delete the archives it refers to; a direct GET is far stronger.
func deleteOrphan(
	ctx context.Context,
	store storage.Storage,
	orphan Orphan,
) (bool, error) {
	backupID, _, ok := storage.ArchiveBackupID(orphan.Key)
	if !ok {
		// Unparsable keys never enter the plan.
		return true, nil
	}

	_, err := storage.GetBytes(ctx, store, storage.ManifestKey(backupID))
	switch {
	case err == nil:
		// The manifest exists after all: the archive is in use, not dangling.
		return true, nil
	case !errors.Is(err, storage.ErrKeyNotFound):
		return false, fmt.Errorf(
			"failed to re-check manifest of archive %q: %w", orphan.Key, err,
		)
	}

	if err := store.Delete(ctx, orphan.Key); err != nil {
		return false, fmt.Errorf("failed to delete archive %q: %w", orphan.Key, err)
	}

	return false, nil
}

// withDefaults fills in the reference time and the orphan age.
func (o Options) withDefaults() Options {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}

	if o.OrphanAge <= 0 {
		o.OrphanAge = DefaultOrphanAge
	}

	return o
}

// hasRetentionRule reports whether the caller asked for anything to be deleted.
// Without a rule every backup would match "not kept", so gc deletes nothing.
func (o Options) hasRetentionRule() bool {
	return o.KeepFull > 0 || o.KeepDays > 0
}

// keepsByAge reports whether a manifest is young enough to be kept. A manifest
// without a creation time is kept: the schema does not require the field, and
// reading its zero value as "created in year one" would delete a backup written
// minutes ago.
func (o Options) keepsByAge(manifest *backup.ClusterManifest) bool {
	if o.KeepDays <= 0 {
		return false
	}

	if manifest.CreationTime.IsZero() {
		return true
	}

	cutoff := o.Now.AddDate(0, 0, -o.KeepDays)

	return !manifest.CreationTime.Before(cutoff)
}
