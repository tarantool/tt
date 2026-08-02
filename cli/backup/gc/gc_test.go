package gc

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/storage"
)

const day = 24 * time.Hour

func TestGcWithoutRetentionRuleIsNoOp(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day, 59*day)
	f.addChain("2026-02-01", 30*day)
	f.addArchive("2026-01-15-dangling", 60*day)
	before := f.store.keys()

	plan, result := f.run(Options{OrphanAge: time.Hour})

	require.True(t, plan.Empty())
	require.Zero(t, result.Backups)
	require.Zero(t, result.Orphans)
	require.Equal(t, before, f.store.keys())
	require.Empty(t, f.store.deletes)
	require.True(t, containsNote(plan, "no retention rule given"))
}

func TestGcKeepFullDeletesOlderChains(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	f.addChain("2026-02-01", 30*day)
	f.addChain("2026-02-20", 10*day)

	plan, result := f.run(Options{KeepFull: 1})

	// The newest chain is the head and is kept anyway; --keep-full 1 keeps it as
	// the single retained chain, so both older chains go.
	require.Equal(t, []string{"2026-01-01", "2026-02-01"}, deletedBackupIDs(plan))
	require.Equal(t, 2, result.Backups)
	require.Equal(t, 2, result.Archives)
}

func TestGcCascadesOverWholeChain(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day, 59*day, 58*day)
	f.addChain("2026-02-01", 30*day)

	plan, _ := f.run(Options{KeepFull: 1})

	// Deleting the full backup takes every increment based on it, newest first.
	require.Equal(t, []string{
		"2026-01-01-inc2", "2026-01-01-inc1", "2026-01-01",
	}, deletedBackupIDs(plan))
	require.Equal(t, []string{
		storage.ManifestKey("2026-01-01-inc2"),
		storage.ArchiveKey("2026-01-01-inc2", replicasetA),
		storage.ManifestKey("2026-01-01-inc1"),
		storage.ArchiveKey("2026-01-01-inc1", replicasetA),
		storage.ManifestKey("2026-01-01"),
		storage.ArchiveKey("2026-01-01", replicasetA),
	}, f.store.deletes)
}

func TestGcKeepDaysHoldsTheBackupsAKeptIncrementNeeds(t *testing.T) {
	f := newFixture(t)
	// Only inc3 falls inside the retention window; it is unrecoverable without
	// the full backup and the increments before it, so the whole chain stays.
	f.addChain("2026-01-01", 30*day, 25*day, 20*day, 2*day)
	f.addChain("2026-02-25", 1*day)

	plan, result := f.run(Options{KeepDays: 7})

	require.True(t, plan.Empty())
	require.Zero(t, result.Backups)
	require.Empty(t, f.store.deletes)
}

func TestGcKeepDaysCutsTheTailAfterTheLastKeptBackup(t *testing.T) {
	f := newFixture(t)
	// inc1 is inside the window, inc2 and inc3 are not: the chain is cut after
	// inc1, newest first.
	f.addChain("2026-01-01", 30*day, 2*day)
	f.addBackup(backupSpec{
		id: "2026-01-01-inc2", previous: "2026-01-01-inc1", base: "2026-01-01",
		backupType: backup.BackupTypeIncremental, age: 20 * day,
		vclockBegin: 200, vclockEnd: 300,
	})
	f.addBackup(backupSpec{
		id: "2026-01-01-inc3", previous: "2026-01-01-inc2", base: "2026-01-01",
		backupType: backup.BackupTypeIncremental, age: 19 * day,
		vclockBegin: 300, vclockEnd: 400,
	})
	f.addChain("2026-02-25", 1*day)

	plan, _ := f.run(Options{KeepDays: 7})

	require.Equal(t, []string{"2026-01-01-inc3", "2026-01-01-inc2"}, deletedBackupIDs(plan))
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01"))
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01-inc1"))
}

func TestGcKeepRulesCombine(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	f.addChain("2026-02-01", 30*day)
	f.addChain("2026-02-25", 1*day)

	// --keep-full 1 alone would delete both older chains, but --keep-days 45
	// still covers the middle one: a backup goes only when no rule keeps it.
	plan, _ := f.run(Options{KeepFull: 1, KeepDays: 45})

	require.Equal(t, []string{"2026-01-01"}, deletedBackupIDs(plan))
}

func TestGcNeverDeletesTheHeadChain(t *testing.T) {
	f := newFixture(t)
	// A single chain, far outside the retention window: gc still keeps it.
	f.addChain("2026-01-01", 60*day, 59*day)

	plan, result := f.run(Options{KeepDays: 7})

	require.True(t, plan.Empty())
	require.Zero(t, result.Backups)
	require.Empty(t, f.store.deletes)
	require.True(t, containsNote(plan, `chain "2026-01-01" is kept`))
	require.True(t, containsNote(plan, "newest manifest"))
	require.True(t, containsNote(plan, "never deletes every chain"))
}

func TestGcProtectsTheNewestRecoverableChain(t *testing.T) {
	f := newFixture(t)
	// The newest manifest belongs to a tail whose full backup is gone (an expiry
	// rule ate it). Protecting only that one would delete every backup that can
	// still be recovered from and keep the one that cannot.
	f.addChain("2026-01-01", 60*day, 59*day)
	f.addBackup(backupSpec{
		id: "2026-03-01-orphan", previous: "2026-02-28", base: "2026-02-28",
		backupType: backup.BackupTypeIncremental, age: 1 * day,
		vclockBegin: 100, vclockEnd: 200,
	})

	// A generous --orphan-age keeps the tail out of the stale-tail rule, so the
	// only thing under test is which chain the protections land on.
	plan, result := f.run(Options{KeepDays: 7, OrphanAge: 3 * day})

	require.True(t, plan.Empty(), "plan: %+v", plan.Backups)
	require.Zero(t, result.Backups)
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01"))
	require.True(t, containsNote(plan, "only chain that can still be recovered"))
}

func TestGcCollectsAStaleTailEvenWhenItHoldsTheNewestManifest(t *testing.T) {
	f := newFixture(t)
	// Head protection exists because an upload may be appending to the chain.
	// Nothing appends to a chain whose full backup is gone and whose manifests
	// are all older than --orphan-age, so protecting it would leak forever.
	f.addChain("2026-01-01", 60*day)
	f.addBackup(backupSpec{
		id: "2026-03-01-orphan", previous: "2026-02-28", base: "2026-02-28",
		backupType: backup.BackupTypeIncremental, age: 40 * day,
		vclockBegin: 100, vclockEnd: 200,
	})

	plan, _ := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Equal(t, []string{"2026-03-01-orphan"}, deletedBackupIDs(plan))
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01"))
}

func TestGcInterruptedOnTheNewestManifestLeavesCollectableGarbage(t *testing.T) {
	f := newFixture(t)
	// The run above, killed between the manifest delete and the archive delete.
	// The archive is left with the highest backup id in the storage, so the
	// in-flight shield would protect it forever: nothing is going to upload that
	// id again, and no other rule ever looks at an archive of a deleted backup.
	f.addChain("2026-01-01", 60*day)
	f.addBackup(backupSpec{
		id: "2026-03-01-orphan", previous: "2026-02-28", base: "2026-02-28",
		backupType: backup.BackupTypeIncremental, age: 40 * day,
		vclockBegin: 100, vclockEnd: 200,
	})
	stranded := storage.ArchiveKey("2026-03-01-orphan", replicasetA)
	f.removeObject(storage.ManifestKey("2026-03-01-orphan"))

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Equal(t, []string{stranded}, orphanKeys(plan))
	require.Equal(t, 1, result.Orphans)
	require.NotContains(t, f.store.keys(), stranded)
}

func TestGcDeletesASharedArchiveOnlyOnce(t *testing.T) {
	f := newFixture(t)
	f.addBackup(backupSpec{
		id: "2026-01-01", base: "2026-01-01", backupType: backup.BackupTypeFull,
		age: 60 * day, vclockEnd: 100,
	})
	second := f.addBackup(backupSpec{
		id: "2026-01-02", base: "2026-01-02", backupType: backup.BackupTypeFull,
		age: 59 * day, vclockEnd: 100,
	})
	// Both doomed manifests name one archive: it is deleted, once.
	shared := storage.ArchiveKey("2026-01-01", replicasetA)
	second.Shards[replicasetA].Instance.Artifact.Path = shared
	f.putObject(storage.ManifestKey("2026-01-02"), f.encode(second), testNow)
	f.addChain("2026-02-25", 1*day)

	_, result := f.run(Options{KeepDays: 7})

	require.Equal(t, 2, result.Backups)
	require.Equal(t, 1, result.Archives)
	require.Equal(t, 1, strings.Count(strings.Join(f.store.deletes, "\n"), shared))
}

func TestGcDeletesOldChainButKeepsTheHeadUnderTheSameRules(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	f.addChain("2026-01-15", 50*day)

	plan, _ := f.run(Options{KeepDays: 7})

	require.Equal(t, []string{"2026-01-01"}, deletedBackupIDs(plan))
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-15"))
}

func TestGcDeletesManifestBeforeItsArchives(t *testing.T) {
	f := newFixture(t)
	f.addBackup(backupSpec{
		id: "2026-01-01", base: "2026-01-01", backupType: backup.BackupTypeFull,
		age: 60 * day, vclockEnd: 100,
		replicasets: []string{replicasetA, replicasetB},
	})
	f.addChain("2026-02-25", 1*day)

	f.run(Options{KeepDays: 7})

	// The manifest goes first: archives without a manifest are collected by the
	// next run, a manifest without archives breaks recovery.
	require.Equal(t, []string{
		storage.ManifestKey("2026-01-01"),
		storage.ArchiveKey("2026-01-01", replicasetA),
		storage.ArchiveKey("2026-01-01", replicasetB),
	}, f.store.deletes)
}

func TestGcDryRunPlanIsNotExecuted(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day, 59*day)
	f.addChain("2026-02-25", 1*day)
	f.addArchive("2026-01-15-dangling", 30*day)
	before := f.store.keys()

	plan := f.plan(Options{KeepDays: 7})

	require.False(t, plan.Empty())
	require.Equal(t, before, f.store.keys())
	require.Empty(t, f.store.deletes)
}

func TestGcDeletesDanglingArchiveOlderThanOrphanAge(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-02-25", 1*day)
	old := f.addArchive("2026-01-15-abandoned", 30*day)
	young := f.addArchive("2026-02-24-abandoned", 2*time.Hour)

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Equal(t, []string{old}, orphanKeys(plan))
	require.Equal(t, 1, result.Orphans)
	require.NotContains(t, f.store.keys(), old)
	require.Contains(t, f.store.keys(), young)
}

func TestGcKeepsArchivesOfInvalidManifest(t *testing.T) {
	// A structurally invalid manifest cannot say which archives it still refers
	// to, so its archives look unreferenced without being abandoned. Collecting
	// them would delete the live data of a backup that exists - and unlike every
	// other command here, gc cannot take it back.
	f := newFixture(t)
	f.addChain("2026-02-25", 1*day)

	broken := f.addBackup(backupSpec{
		id: "2026-01-10-broken", previous: "", base: "2026-01-10-broken",
		backupType: backup.BackupTypeFull, age: 30 * day,
	})
	// Readable and well-formed JSON, but not a usable manifest.
	broken.Shards = map[string]backup.Shard{}
	f.putObject(storage.ManifestKey(string(broken.BackupID)), f.encode(broken),
		testNow.Add(-30*day))

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Empty(t, orphanKeys(plan))
	require.Zero(t, result.Orphans)
	require.Contains(t, f.store.keys(), storage.ArchiveKey(string(broken.BackupID), replicasetA))
}

func TestGcKeepsArchiveNewerThanEveryManifest(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	// An upload in progress: archives land before the manifest that refers to
	// them, so an archive written within --orphan-age whose backup id is above
	// every manifest is not an orphan. Age still has the last word - see
	// TestGcInterruptedOnTheNewestManifestLeavesCollectableGarbage - so an
	// upload slower than --orphan-age needs a wider window, not a wider id rule.
	inFlight := f.addArchive("2026-02-28-uploading", 30*time.Minute)

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: time.Hour})

	require.Empty(t, orphanKeys(plan))
	require.Zero(t, result.Orphans)
	require.Contains(t, f.store.keys(), inFlight)
	require.True(t, containsNote(plan, "newer than the newest manifest"))
}

func TestGcRechecksManifestBeforeDeletingAnOrphan(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-02-25", 1*day)
	// The manifest of this backup exists but the listing does not show it yet:
	// its archive looks dangling and must survive the direct read.
	lagging := f.addBackup(backupSpec{
		id: "2026-02-20", base: "2026-02-20", backupType: backup.BackupTypeFull,
		age: 30 * day, vclockEnd: 100,
	})
	manifestKey := storage.ManifestKey(string(lagging.BackupID))
	f.store.hiddenFromList[manifestKey] = true
	archiveKey := storage.ArchiveKey(string(lagging.BackupID), replicasetA)

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Equal(t, []string{archiveKey}, orphanKeys(plan))
	require.Zero(t, result.Orphans)
	require.Equal(t, []string{archiveKey}, result.Kept)
	require.Contains(t, f.store.gets, manifestKey)
	require.Contains(t, f.store.keys(), archiveKey)
	require.Empty(t, f.store.deletes)
}

func TestGcKeepsForkedChain(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day, 59*day)
	// A second increment on the same parent: the chain forks and gc leaves the
	// whole diagnostic picture to tt backup verify.
	f.addBackup(backupSpec{
		id: "2026-01-01-fork", previous: "2026-01-01", base: "2026-01-01",
		backupType: backup.BackupTypeIncremental, age: 58 * day,
		vclockBegin: 100, vclockEnd: 200,
	})
	f.addChain("2026-02-25", 1*day)

	plan, _ := f.run(Options{KeepDays: 7, KeepFull: 1})

	require.True(t, plan.Empty())
	require.True(t, containsNote(plan, "chain problems"))
}

func TestGcKeepsForkedChainOutOfTheKeepFullCount(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	f.addChain("2026-01-15", 50*day)
	// A broken chain between the healthy ones must not consume the keep-full slot
	// that would otherwise protect 2026-01-15. It is young, so the stale-tail
	// rule does not fire either.
	f.addBackup(backupSpec{
		id: "2026-02-01-orphan", previous: "vanished", base: "2026-02-01",
		backupType: backup.BackupTypeIncremental, age: 2 * time.Hour,
		vclockBegin: 100, vclockEnd: 200,
	})
	f.addChain("2026-02-25", 1*day)

	plan, _ := f.run(Options{KeepFull: 2})

	require.Equal(t, []string{"2026-01-01"}, deletedBackupIDs(plan))
}

func TestGcDeletesStaleOrphanTail(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-02-25", 1*day)
	// An increment whose full backup is gone: nothing can be recovered from it,
	// and no other rule would ever collect it.
	f.addBackup(backupSpec{
		id: "2026-01-05-orphan", previous: "2026-01-01", base: "2026-01-01",
		backupType: backup.BackupTypeIncremental, age: 40 * day,
		vclockBegin: 100, vclockEnd: 200,
	})

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Equal(t, []string{"2026-01-05-orphan"}, deletedBackupIDs(plan))
	require.Equal(t, 1, result.Backups)
}

func TestGcKeepsFreshOrphanTail(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-02-25", 1*day)
	// The same tail, but young enough to be the increment of an upload whose
	// full backup manifest has not landed yet.
	f.addBackup(backupSpec{
		id: "2026-02-26-orphan", previous: "2026-02-24", base: "2026-02-24",
		backupType: backup.BackupTypeIncremental, age: 2 * time.Hour,
		vclockBegin: 100, vclockEnd: 200,
	})

	plan, _ := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Empty(t, deletedBackupIDs(plan))
}

func TestGcKeepsArchiveOutsideTheKeyLayout(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	f.putObject("data/README.txt", []byte("not an archive"), testNow.Add(-60*day))

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: day})

	require.Empty(t, orphanKeys(plan))
	require.Zero(t, result.Orphans)
	require.Contains(t, f.store.keys(), "data/README.txt")
	require.True(t, containsNote(plan, "layout"))
}

func TestGcKeepsEveryArchiveWhenNoManifestIsStoredYet(t *testing.T) {
	f := newFixture(t)
	// The first backup of a new cluster: archives are uploaded before the
	// manifest, so an empty manifests/ means "an upload may be in flight", not
	// "everything here is garbage".
	first := f.addArchive("2026-02-28-first", 30*day)

	plan, result := f.run(Options{KeepFull: 1, OrphanAge: time.Hour})

	require.Empty(t, orphanKeys(plan))
	require.Zero(t, result.Orphans)
	require.Contains(t, f.store.keys(), first)
}

func TestGcKeepsBackupWithoutCreationTime(t *testing.T) {
	f := newFixture(t)
	// Every retention rule reads creation_time, so a manifest without one has no
	// defined age. It is an invalid manifest, which makes its chain problematic
	// and puts it out of gc's reach: verify reports it, gc leaves it alone.
	f.addChain("2026-02-25", 1*day)
	f.addBackup(backupSpec{
		id: "2026-01-01", base: "2026-01-01", backupType: backup.BackupTypeFull,
		vclockEnd: 100, noCreationTime: true,
	})

	plan, _ := f.run(Options{KeepDays: 7, KeepFull: 1})

	require.Empty(t, deletedBackupIDs(plan))
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01"))
	require.True(t, containsNote(plan, "chain problems"))
}

func TestGcKeepsAChainWhoseAgeCannotBeEstablished(t *testing.T) {
	// The retention rules themselves must not read a missing creation time as
	// "ancient", whatever validation does upstream.
	manifest := &backup.ClusterManifest{CreationTime: time.Time{}}
	opts := Options{KeepDays: 7, Now: testNow}

	require.True(t, opts.keepsByAge(manifest))
	require.False(t, isStale(group{entries: []*chain.Entry{{Manifest: manifest}}},
		Options{OrphanAge: day, Now: testNow}))
}

func TestGcKeepsArchiveASurvivorStillRefersTo(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	survivor := f.addBackup(backupSpec{
		id: "2026-02-25", base: "2026-02-25", backupType: backup.BackupTypeFull,
		age: 1 * day, vclockEnd: 100,
	})
	// The surviving manifest points at the doomed backup's archive, so the
	// archive has to outlive the backup that named it first.
	shared := storage.ArchiveKey("2026-01-01", replicasetA)
	survivor.Shards[replicasetA].Instance.Artifact.Path = shared
	f.putObject(storage.ManifestKey("2026-02-25"), f.encode(survivor), testNow)

	plan, _ := f.run(Options{KeepDays: 7})

	require.Equal(t, []string{"2026-01-01"}, deletedBackupIDs(plan))
	require.Empty(t, plan.Backups[0].ArchiveKeys)
	require.Contains(t, f.store.keys(), shared)
	require.True(t, containsNote(plan, "still refers to it"))
}

func TestGcRefusesToDeleteAKeyOutsideTheDataPrefix(t *testing.T) {
	f := newFixture(t)
	doomed := f.addBackup(backupSpec{
		id: "2026-01-01", base: "2026-01-01", backupType: backup.BackupTypeFull,
		age: 60 * day, vclockEnd: 100,
	})
	f.addChain("2026-02-20", 10*day)
	// Nothing validates artifact.path, so a hand-edited or third-party manifest
	// can name any object in the storage - here the surviving chain's manifest,
	// the one object gc must never touch on its way to deleting an archive.
	survivor := storage.ManifestKey("2026-02-20")
	doomed.Shards[replicasetA].Instance.Artifact.Path = survivor
	f.putObject(storage.ManifestKey("2026-01-01"), f.encode(doomed), testNow.Add(-60*day))

	plan, result := f.run(Options{KeepFull: 1})

	require.Equal(t, []string{"2026-01-01"}, deletedBackupIDs(plan))
	require.Empty(t, plan.Backups[0].ArchiveKeys)
	require.Zero(t, plan.Archives())
	require.Zero(t, result.Archives)
	require.Contains(t, f.store.keys(), survivor)
	require.True(t, containsNote(plan, survivor))
}

func TestGcExecuteNeverDeletesOutsideDataPrefix(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	f.addChain("2026-02-20", 10*day)
	// However the plan was built, Execute is the last place that can refuse.
	plan := &Plan{Backups: []Backup{{
		BackupID:    "2026-01-01",
		ManifestKey: storage.ManifestKey("2026-01-01"),
		ArchiveKeys: []string{storage.ManifestKey("2026-02-20"), "random/key"},
	}}}

	result, err := Execute(t.Context(), f.store, plan)

	require.ErrorContains(t, err, storage.ManifestKey("2026-02-20"))
	require.Zero(t, result.Backups)
	require.Zero(t, result.Archives)

	// The same invariant covers the two other kinds of key a plan carries.
	_, err = Execute(t.Context(), f.store, &Plan{Backups: []Backup{{
		BackupID: "2026-01-01", ManifestKey: "random/manifest.json",
	}}})
	require.ErrorContains(t, err, "random/manifest.json")

	_, err = Execute(t.Context(), f.store, &Plan{
		Orphans: []Orphan{{Key: storage.ManifestKey("2026-02-20")}},
	})
	require.ErrorContains(t, err, storage.ManifestKey("2026-02-20"))

	require.False(t, slices.ContainsFunc(f.store.deletes, func(key string) bool {
		return !strings.HasPrefix(key, storage.DataPrefix())
	}), "deleted outside %s: %v", storage.DataPrefix(), f.store.deletes)
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-02-20"))
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01"))
}

func TestGcKeepsBackupASurvivorIsRecoveredThrough(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)
	// An increment that declares another chain as its base but is recovered
	// through this one. chain.Build does not call that a problem, so the plan
	// would delete the backup this increment replays.
	f.addBackup(backupSpec{
		id: "2026-02-25", previous: "2026-01-01", base: "2026-02-25",
		backupType: backup.BackupTypeIncremental, age: 1 * day,
		vclockBegin: 100, vclockEnd: 200,
	})

	plan, _ := f.run(Options{KeepDays: 7})

	require.Empty(t, deletedBackupIDs(plan))
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01"))
	require.True(t, containsNote(plan, "recovered through it"))
}

func TestGcAlwaysSaysItCannotEmptyTheStorage(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day)

	// Both the flagless no-op and a run with rules owe the user the sentence.
	require.True(t, containsNote(f.plan(Options{}), "never deletes every chain"))
	require.True(t, containsNote(f.plan(Options{KeepFull: 1}), "never deletes every chain"))
}

func TestGcFailsOnUnreadableChain(t *testing.T) {
	f := newFixture(t)
	f.putObject(storage.ManifestKey("broken"), []byte("{"), testNow)

	_, err := BuildPlan(t.Context(), f.store, Options{KeepFull: 1, Now: testNow})

	require.ErrorContains(t, err, "decode manifest")
	require.Empty(t, f.store.deletes)
}

func TestGcFailsOnListError(t *testing.T) {
	f := newFixture(t)
	f.store.listErr = errors.New("storage is unreachable")

	_, err := BuildPlan(t.Context(), f.store, Options{KeepFull: 1, Now: testNow})

	require.ErrorContains(t, err, "storage is unreachable")
}

func TestGcReportsWhatItDeletedWhenDeletionFails(t *testing.T) {
	f := newFixture(t)
	f.addChain("2026-01-01", 60*day, 59*day)
	f.addChain("2026-02-25", 1*day)
	failing := storage.ArchiveKey("2026-01-01-inc1", replicasetA)
	f.store.deleteErr[failing] = errors.New("permission denied")

	plan := f.plan(Options{KeepDays: 7})
	result, err := Execute(t.Context(), f.store, plan)

	require.ErrorContains(t, err, "permission denied")
	require.Equal(t, 1, result.Backups)
	require.Zero(t, result.Archives)
	// The full backup is still there: an interrupted run leaves a valid prefix.
	require.Contains(t, f.store.keys(), storage.ManifestKey("2026-01-01"))
}

func TestGcDefaultOrphanAgeOutlivesAnUpload(t *testing.T) {
	require.GreaterOrEqual(t, DefaultOrphanAge, day)
}
