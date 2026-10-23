package restore

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/backup/storage"
	"github.com/tarantool/tt/v3/cli/backup"
	"github.com/tarantool/tt/v3/cli/backup/chain"
)

// TestPlanStatusStringsMatchTheSpecTable pins the six status names. They are
// the orchestrator's branch point - each maps to its own exit code - so a
// rename here is a breaking change for every caller of the command.
func TestPlanStatusStringsMatchTheSpecTable(t *testing.T) {
	require.Equal(t, StatusOK, Status("ok"))
	require.Equal(t, StatusTopologyBoundary, Status("topology_boundary"))
	require.Equal(t, StatusNoRecoveryPoint, Status("no_recovery_point"))
	require.Equal(t, StatusChainBroken, Status("chain_broken"))
	require.Equal(t, StatusOutOfRange, Status("out_of_range"))
	// The sixth is this command's own: the chain is pure over manifests and
	// never sees the cluster a restore is aimed at.
	require.Equal(t, StatusTopologyMismatch, Status("topology_mismatch"))

	// The five that come from resolving are produced by chain.Status.String(),
	// so the two tables cannot drift apart.
	require.Equal(t, StatusOK, Status(chain.StatusOK.String()))
	require.Equal(t, StatusTopologyBoundary, Status(chain.StatusTopologyBoundary.String()))
	require.Equal(t, StatusNoRecoveryPoint, Status(chain.StatusNoRecoveryPoint.String()))
	require.Equal(t, StatusChainBroken, Status(chain.StatusChainBroken.String()))
	require.Equal(t, StatusOutOfRange, Status(chain.StatusOutOfRange.String()))
}

func TestPlanOKDownloadsTheChainInOrder(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	result := fixture.plan(550, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)
	require.Empty(t, result.Reason)
	require.Nil(t, result.NearestSafe)
	require.Nil(t, result.TopologyDiff)
	// The manifests of the standard chain record no members, so each
	// replicaset is planned as a single-target restore and says so.
	require.Equal(t, []string{
		noMembersWarning(incTwoID, shardA),
		noMembersWarning(incTwoID, shardB),
	}, result.Warnings)
	require.Equal(t, time.Unix(550, 0).UTC(), result.TargetTime)

	// The cluster point is the latest not later than the target, and its time is
	// the earliest of its shards' - the moment every shard is guaranteed to have.
	require.NotNil(t, result.RecoveryPoint)
	require.Equal(t, "p3", result.RecoveryPoint.Label)
	require.Equal(t, time.Unix(510, 0).UTC(), result.RecoveryPoint.Timestamp)
	require.Equal(t, map[string]Point{
		shardA: {ReplicaID: replicaOfA, LSN: 2500},
		shardB: {ReplicaID: replicaOfB, LSN: 2000},
	}, result.RecoveryPoint.TrimToByReplicaset)

	require.Len(t, result.DownloadPlan, 2)

	chainOfA := result.DownloadPlan[shardA]
	require.Equal(t, []string{fullBackupID, incOneID, incTwoID}, itemIDs(chainOfA))
	require.Equal(t, []backup.BackupType{
		backup.BackupTypeFull,
		backup.BackupTypeIncremental,
		backup.BackupTypeIncremental,
	}, itemTypes(chainOfA))

	chainOfB := result.DownloadPlan[shardB]
	require.Equal(t, []string{fullBackupID, incOneID, incTwoID}, itemIDs(chainOfB))

	// Only the archive holding the recovery point is trimmed: everything before
	// it is replayed whole.
	for _, item := range chainOfA[:2] {
		require.False(t, item.TrimXlog, "backup %s is not the last of the chain", item.BackupID)
		require.Nil(t, item.TrimTo)
	}

	require.True(t, chainOfA[2].TrimXlog)
	require.Equal(t, &Point{ReplicaID: replicaOfA, LSN: 2500}, chainOfA[2].TrimTo)
	require.True(t, chainOfB[2].TrimXlog)
	require.Equal(t, &Point{ReplicaID: replicaOfB, LSN: 2000}, chainOfB[2].TrimTo)

	// The final trim is the point itself: what apply cuts each replicaset's
	// journal at has to be what the recovery point says, or the shards come back
	// to different moments.
	for replicasetUUID, items := range result.DownloadPlan {
		trim := result.RecoveryPoint.TrimToByReplicaset[replicasetUUID]
		require.Equal(t, &trim, items[len(items)-1].TrimTo)
	}

	for _, item := range chainOfA {
		fixture.requireLocalCopy(item, shardA)
	}

	for _, item := range chainOfB {
		fixture.requireLocalCopy(item, shardB)
	}

	fixture.requireNoPartials()
}

func TestPlanDownloadsTheManifestsOfTheChain(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	fixture.plan(550, masterOnlyCluster())

	expected := make([]string, 0, 9)

	expected = append(expected, []string{
		manifestName(fullBackupID), manifestName(incOneID), manifestName(incTwoID),
	}...)

	for _, backupID := range []string{fullBackupID, incOneID, incTwoID} {
		expected = append(expected,
			archiveName(backupID, shardA), archiveName(backupID, shardB))
	}

	slices.Sort(expected)
	require.Equal(t, expected, fixture.downloaded())

	// Every manifest is fetched before any archive: a chain whose metadata
	// cannot be read is not worth spending an archive download on.
	firstArchive := slices.IndexFunc(fixture.store.gets, func(key string) bool {
		return strings.HasPrefix(key, storage.DataPrefix())
	})
	require.GreaterOrEqual(t, firstArchive, 0, "no archive was ever fetched")

	for _, key := range fixture.store.gets[firstArchive:] {
		require.NotContains(t, key, storage.ManifestsPrefix(),
			"manifest %q was fetched after an archive", key)
	}
}

func TestPlanStopsTheChainAtTheBackupCarryingThePoint(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	// The target lands on the middle point, so the last backup is past the
	// moment being restored to and has no business being downloaded.
	result := fixture.plan(350, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, "p2", result.RecoveryPoint.Label)
	require.Equal(t, []string{fullBackupID, incOneID}, itemIDs(result.DownloadPlan[shardA]))
	require.Equal(t, []string{fullBackupID, incOneID}, itemIDs(result.DownloadPlan[shardB]))

	require.Equal(t, &Point{ReplicaID: replicaOfA, LSN: 1502},
		result.DownloadPlan[shardA][1].TrimTo)

	downloaded := fixture.downloaded()
	require.NotContains(t, downloaded, manifestName(incTwoID))
	require.NotContains(t, downloaded, archiveName(incTwoID, shardA))
	require.NotContains(t, downloaded, archiveName(incTwoID, shardB))
}

// TestPlanSkipsBackupsThatDoNotCarryTheShard reproduces the RFC's own example,
// where one replicaset was unreachable for the middle backup: its chain jumps
// straight from the full to the last increment, while the other replicaset
// replays all three.
func TestPlanSkipsBackupsThatDoNotCarryTheShard(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.chainWithAnUnreachableShard()

	result := fixture.plan(550, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, "p3", result.RecoveryPoint.Label)
	require.Equal(t, []string{fullBackupID, incOneID, incTwoID},
		itemIDs(result.DownloadPlan[shardA]))
	require.Equal(t, []string{fullBackupID, incTwoID}, itemIDs(result.DownloadPlan[shardB]))

	// The skipped backup's archive of that replicaset was never stored, and the
	// plan must not have gone looking for it either.
	require.NotContains(t, fixture.downloaded(), archiveName(incOneID, shardB))
	require.NotContains(t, fixture.store.gets, storage.ArchiveKey(incOneID, shardB))

	for _, item := range result.DownloadPlan[shardB] {
		fixture.requireLocalCopy(item, shardB)
	}
}

func TestPlanTopologyBoundary(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.chainAcrossAMasterChange()

	// The target falls between the last point of the old topology and the first
	// of the new one: there is no single cluster state to return to in there.
	result := fixture.plan(400, masterOnlyCluster())

	require.Equal(t, StatusTopologyBoundary, result.Status)
	require.Equal(t, "target time falls between manifests with different topology",
		result.Reason)
	require.Nil(t, result.RecoveryPoint)
	require.Empty(t, result.DownloadPlan)
	require.NotNil(t, result.NearestSafe)
	require.Equal(t, time.Unix(110, 0).UTC(), *result.NearestSafe.Before)
	require.Equal(t, time.Unix(510, 0).UTC(), *result.NearestSafe.After)

	require.Empty(t, fixture.downloaded(), "a plan that cannot go ahead downloads nothing")
}

// TestPlanTopologyBoundaryAcrossAMembershipChange covers the boundary a
// member, not a master, makes: the replicaset is backed up on the same
// instance on both sides, but it grew a member in between, and a cluster
// that was two nodes at one moment and three at the next has no single
// membership to restore under. A masters-only check would stitch straight
// through it.
func TestPlanTopologyBoundaryAcrossAMembershipChange(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.chainAcrossAMembershipChange()

	result := fixture.plan(400, masterOnlyCluster())

	require.Equal(t, StatusTopologyBoundary, result.Status)
	require.Equal(t, "target time falls between manifests with different topology",
		result.Reason)
	require.Nil(t, result.RecoveryPoint)
	require.Empty(t, result.DownloadPlan)
	require.NotNil(t, result.NearestSafe)
	require.Equal(t, time.Unix(110, 0).UTC(), *result.NearestSafe.Before)
	require.Equal(t, time.Unix(510, 0).UTC(), *result.NearestSafe.After)

	require.Empty(t, fixture.downloaded(), "a plan that cannot go ahead downloads nothing")
}

func TestPlanNoRecoveryPoint(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.chainWithALateFirstPoint()

	// 250 is inside what the storage covers - the full backup was taken at 200 -
	// but the first moment every shard agrees on only comes at 310.
	result := fixture.plan(250, masterOnlyCluster())

	require.Equal(t, StatusNoRecoveryPoint, result.Status)
	require.Equal(t, "no cluster recovery point is available around the target time",
		result.Reason)
	require.Nil(t, result.RecoveryPoint)
	require.Empty(t, result.DownloadPlan)
	require.NotNil(t, result.NearestSafe)
	require.Nil(t, result.NearestSafe.Before)
	require.Equal(t, time.Unix(310, 0).UTC(), *result.NearestSafe.After)

	require.Empty(t, fixture.downloaded())
}

func TestPlanChainBroken(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.twoUnrelatedFullBackups()

	// The target falls between two full backups that stand on nothing common:
	// no chain leads from the earlier one to the later.
	result := fixture.plan(300, masterOnlyCluster())

	require.Equal(t, StatusChainBroken, result.Status)
	require.Equal(t, "the backup chain is broken around the target time", result.Reason)
	require.Nil(t, result.RecoveryPoint)
	require.Empty(t, result.DownloadPlan)
	require.NotNil(t, result.NearestSafe)
	require.Equal(t, time.Unix(110, 0).UTC(), *result.NearestSafe.Before)
	require.Equal(t, time.Unix(510, 0).UTC(), *result.NearestSafe.After)

	require.Empty(t, fixture.downloaded())
}

func TestPlanOutOfRangeAfterTheLastPoint(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	result := fixture.plan(900, masterOnlyCluster())

	require.Equal(t, StatusOutOfRange, result.Status)
	require.Equal(t, "target time is outside the coverage of the stored manifests",
		result.Reason)
	require.Nil(t, result.RecoveryPoint)
	require.Empty(t, result.DownloadPlan)
	require.NotNil(t, result.NearestSafe)
	require.Equal(t, time.Unix(610, 0).UTC(), *result.NearestSafe.Before)
	require.Nil(t, result.NearestSafe.After)

	require.Empty(t, fixture.downloaded())
}

func TestPlanOutOfRangeBeforeTheFirstManifest(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	// Earlier than every stored manifest: this storage holds nothing for that
	// moment, which is a different problem from holding backups with no point.
	result := fixture.plan(50, masterOnlyCluster())

	require.Equal(t, StatusOutOfRange, result.Status)
	require.Nil(t, result.RecoveryPoint)
	require.NotNil(t, result.NearestSafe)
	require.Nil(t, result.NearestSafe.Before)
	require.Equal(t, time.Unix(110, 0).UTC(), *result.NearestSafe.After)

	require.Empty(t, fixture.downloaded())
}

func TestPlanMissingArchiveBlocksThePlan(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	delete(fixture.store.objects, storage.ArchiveKey(incTwoID, shardB))

	result, err := fixture.run(550, masterOnlyCluster())

	require.ErrorIs(t, err, ErrArchiveMissing)
	require.ErrorContains(t, err, storage.ArchiveKey(incTwoID, shardB))
	require.Nil(t, result, "a plan whose archives are incomplete must not be handed out")

	downloaded := fixture.downloaded()
	require.NotContains(t, downloaded, archiveName(incTwoID, shardB))
	fixture.requireNoPartials()
}

func TestPlanCorruptArchiveBlocksThePlan(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	key := storage.ArchiveKey(incTwoID, shardA)

	fixture.store.objects[key] = []byte("something else entirely")

	result, err := fixture.run(550, masterOnlyCluster())

	require.ErrorIs(t, err, ErrChecksumMismatch)
	require.Nil(t, result)

	// Nothing of the archive survives: a file under its final name would be
	// picked up by the next run as an archive already fetched and verified.
	require.NotContains(t, fixture.downloaded(), archiveName(incTwoID, shardA))
	fixture.requireNoPartials()

	// What was already verified stays, whole: those files are what a retry
	// reuses once the corrupt archive has been dealt with.
	stillThere := filepath.Join(fixture.dir, archiveName(fullBackupID, shardA))
	data, err := os.ReadFile(stillThere)
	require.NoError(t, err)
	require.Equal(t, fixture.store.objects[storage.ArchiveKey(fullBackupID, shardA)], data)
}

// TestPlanPointAtTheEndOfTheArchiveTrimsNothing covers a recovery point that
// sits exactly at the end of the journal its archive carries: there is nothing
// past it to cut off, so no archive of the chain is marked for trimming. The
// position is still reported per replicaset, which is what apply is given.
func TestPlanPointAtTheEndOfTheArchiveTrimsNothing(t *testing.T) {
	fixture := newPlanFixture(t)

	fixture.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			shardOfA(0, 1000, recoveryPointAt("p1", replicaOfA, 500, 110)),
			shardOfB(0, 900, recoveryPointAt("p1", replicaOfB, 400, 111)),
		},
	})

	fixture.addBackup(clusterBackup{
		id: incOneID, previous: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeIncremental, createdAt: 400,
		shards: []shardBackup{
			shardOfA(1000, 2000, recoveryPointAt("p2", replicaOfA, 2000, 310)),
			shardOfB(900, 1800, recoveryPointAt("p2", replicaOfB, 1800, 311)),
		},
	})

	result := fixture.plan(310, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, map[string]Point{
		shardA: {ReplicaID: replicaOfA, LSN: 2000},
		shardB: {ReplicaID: replicaOfB, LSN: 1800},
	}, result.RecoveryPoint.TrimToByReplicaset)

	for replicasetUUID, items := range result.DownloadPlan {
		for _, item := range items {
			require.False(t, item.TrimXlog,
				"replicaset %s backup %s", replicasetUUID, item.BackupID)
			require.Nil(t, item.TrimTo)
		}
	}
}

// TestPlanWarnsAboutAnArchiveWithNoRecordedChecksum covers a manifest that
// carries no sum for an archive: nothing verifies the download, so the plan
// says so out loud rather than passing an unchecked archive off as verified.
func TestPlanWarnsAboutAnArchiveWithNoRecordedChecksum(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	manifest := fixture.storedManifest(fullBackupID)
	shard := manifest.Shards[shardA]

	shard.Instance.Artifact.ChecksumSHA256 = ""
	manifest.Shards[shardA] = shard
	fixture.putManifest(manifest)

	result := fixture.plan(550, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)
	// The unchecksummed archive beside the two single-target warnings.
	require.Len(t, result.Warnings, 3)
	require.Equal(t, []string{
		noMembersWarning(incTwoID, shardA),
		noMembersWarning(incTwoID, shardB),
	}, result.Warnings[:2])
	require.Contains(t, result.Warnings[2], "records no checksum")
	require.Contains(t, result.Warnings[2], storage.ArchiveKey(fullBackupID, shardA))

	// The plan still reports a sum: the one of the bytes that were downloaded,
	// so that apply can at least check the copy to the node.
	item := result.DownloadPlan[shardA][0]
	require.Equal(t, sha256Of(archiveBody(fullBackupID, shardA)), item.ChecksumSHA256)
	require.Contains(t, result.Warnings[2], item.ChecksumSHA256)
}

// TestPlanRejectsAnArtifactPathThatIsNotAStorageKey stops a manifest from
// pointing the download anywhere but at an object of this storage.
func TestPlanRejectsAnArtifactPathThatIsNotAStorageKey(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	manifest := fixture.storedManifest(fullBackupID)
	shard := manifest.Shards[shardA]

	shard.Instance.Artifact.Path = "../../etc/passwd"
	manifest.Shards[shardA] = shard
	fixture.putManifest(manifest)

	result, err := fixture.run(550, masterOnlyCluster())

	require.ErrorIs(t, err, storage.ErrInvalidKey)
	require.ErrorContains(t, err, "is not a storage key")
	require.Nil(t, result)
	require.NotContains(t, fixture.downloaded(), "passwd")
}

// TestPlanRejectsTwoArtifactsWithTheSameFileName covers two replicasets whose
// artifact keys differ but end in the same name. The download directory is flat,
// so they would collapse into one file - and silently: each download is verified
// against its own manifest entry before the next one overwrites it, leaving a
// plan that hands a replicaset an archive taken on another one.
func TestPlanRejectsTwoArtifactsWithTheSameFileName(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	manifest := fixture.storedManifest(incTwoID)

	for _, replicasetUUID := range []string{shardA, shardB} {
		key := "data/" + replicasetUUID + "/final.tar.zst"

		fixture.store.objects[key] = archiveBody(incTwoID, replicasetUUID)

		shard := manifest.Shards[replicasetUUID]

		shard.Instance.Artifact.Path = key
		manifest.Shards[replicasetUUID] = shard
	}

	fixture.putManifest(manifest)

	result, err := fixture.run(550, masterOnlyCluster())

	require.ErrorIs(t, err, ErrDestinationTaken)
	require.ErrorContains(t, err, "final.tar.zst")
	require.Nil(t, result, "a plan whose archives share a file must not be handed out")
}

func TestPlanArchiveTruncatedInTransitLeavesNoFile(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	key := storage.ArchiveKey(incOneID, shardA)

	fixture.store.truncated[key] = errStorageDied

	result, err := fixture.run(550, masterOnlyCluster())

	require.ErrorIs(t, err, errStorageDied)
	require.Nil(t, result)

	require.NotContains(t, fixture.downloaded(), archiveName(incOneID, shardA))
	fixture.requireNoPartials()
}

func TestPlanUnreadableManifestBlocksThePlanBeforeAnyArchive(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	fixture.store.objects[storage.ManifestKey(incOneID)] = []byte("{not json")

	result, err := fixture.run(550, masterOnlyCluster())

	require.ErrorContains(t, err, "failed to build the backup chain")
	require.Nil(t, result)

	// The storage is listed and read before anything is downloaded, so a
	// manifest that cannot be decoded costs no archive traffic at all.
	require.Empty(t, fixture.downloaded())

	for _, key := range fixture.store.gets {
		require.NotContains(t, key, storage.DataPrefix())
	}
}

func TestPlanReusesAnArchiveAlreadyDownloaded(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	fixture.plan(550, masterOnlyCluster())

	fixture.store.gets = nil

	result := fixture.plan(550, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)

	// Re-planning a restore is normal and the archives are large, so a local
	// copy whose sum still matches is kept instead of pulled again.
	for _, key := range fixture.store.gets {
		require.NotContains(t, key, storage.DataPrefix(),
			"archive %q was downloaded again although the local copy matched", key)
	}
}

func TestPlanReplacesALocalFileThatDoesNotMatch(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	require.NoError(t, os.MkdirAll(fixture.dir, 0o755))

	stale := filepath.Join(fixture.dir, archiveName(fullBackupID, shardA))
	require.NoError(t, os.WriteFile(stale, []byte("left over from another restore"), 0o644))

	result := fixture.plan(550, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)
	fixture.requireLocalCopy(result.DownloadPlan[shardA][0], shardA)
}

func TestParseTargetTime(t *testing.T) {
	moment, err := ParseTargetTime("2026-03-25T10:30:00Z")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 25, 10, 30, 0, 0, time.UTC), moment)

	// A local-offset timestamp names the same instant, and is normalised to UTC
	// so that the plan reads the same wherever it is printed.
	moment, err = ParseTargetTime("2026-03-25T13:30:00+03:00")
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 25, 10, 30, 0, 0, time.UTC), moment)

	moment, err = ParseTargetTime("1774434600")
	require.NoError(t, err)
	require.Equal(t, time.Unix(1774434600, 0).UTC(), moment)

	_, err = ParseTargetTime("yesterday")
	require.ErrorContains(t, err, "--target-time")
}
