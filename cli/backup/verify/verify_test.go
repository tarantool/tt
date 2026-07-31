package verify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/storage"
)

func TestVerifyEmptyStorage(t *testing.T) {
	report := newFixture(t).verify()

	require.True(t, report.OK())
	require.Empty(t, report.Issues)
	require.Zero(t, report.Manifests)
	require.Zero(t, report.Archives)
}

func TestVerifyHealthyStorageIsSilent(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	f.addBackup("incremental", "full", "full", backup.BackupTypeIncremental, 10, 20)

	report := f.verify()

	require.True(t, report.OK())
	require.Empty(t, report.Issues)
	require.Equal(t, 2, report.Manifests)
	require.Equal(t, 2, report.Archives)
}

func TestVerifyChecksEveryChain(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full-1", "", "full-1", backup.BackupTypeFull, 0, 10)
	f.addBackup("full-2", "", "full-2", backup.BackupTypeFull, 0, 10)
	// Only the archive of the second, unrelated chain is corrupted.
	f.putObject(storage.ArchiveKey("full-2", replicasetA), []byte("corrupted"))

	report := f.verify()

	require.Equal(t, []IssueKind{IssueChecksumMismatch}, issueKinds(report))
	require.Equal(t, "full-2", report.Issues[0].BackupID)
	require.Equal(t, 2, report.Manifests)
}

func TestVerifyMissingArchive(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	key := storage.ArchiveKey("full", replicasetA)
	delete(f.store.objects, key)

	report := f.verify()

	require.False(t, report.OK())
	require.Equal(t, []IssueKind{IssueMissingArchive}, issueKinds(report))

	issue := findIssue(t, report, IssueMissingArchive)
	require.Equal(t, "full", issue.BackupID)
	require.Equal(t, replicasetA, issue.ReplicasetUUID)
	require.Equal(t, key, issue.Archive)
	// The archive is still counted: verify checked it and found it missing.
	require.Equal(t, 1, report.Archives)
}

func TestVerifyEmptyArtifactPath(t *testing.T) {
	f := newFixture(t)
	manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	manifest.Shards[replicasetA].Instance.Artifact.Path = ""
	f.putManifest(manifest)

	report := f.verify()

	issue := findIssue(t, report, IssueMissingArchive)
	require.Contains(t, issue.Detail, "not a storage key")
	// The unreferenced archive is reported as dangling on top of the broken link.
	require.Equal(t,
		[]IssueKind{IssueMissingArchive, IssueDanglingArchive},
		issueKinds(report),
	)
}

func TestVerifyChecksumMismatch(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	key := storage.ArchiveKey("full", replicasetA)
	corrupted := []byte("corrupted archive")
	f.putObject(key, corrupted)

	report := f.verify()

	require.Equal(t, []IssueKind{IssueChecksumMismatch}, issueKinds(report))

	issue := findIssue(t, report, IssueChecksumMismatch)
	require.Equal(t, key, issue.Archive)
	require.Contains(t, issue.Detail, checksumOf(corrupted))
}

func TestVerifyChecksumMissing(t *testing.T) {
	f := newFixture(t)
	manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	manifest.Shards[replicasetA].Instance.Artifact.ChecksumSHA256 = ""
	f.putManifest(manifest)

	report := f.verify()

	issue := findIssue(t, report, IssueChecksumMissing)
	require.Equal(t, storage.ArchiveKey("full", replicasetA), issue.Archive)
}

func TestVerifyChecksumComparisonIsCaseInsensitive(t *testing.T) {
	f := newFixture(t)
	manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	artifact := &manifest.Shards[replicasetA].Instance.Artifact
	artifact.ChecksumSHA256 = strings.ToUpper(artifact.ChecksumSHA256)
	f.putManifest(manifest)

	require.True(t, f.verify().OK())
}

func TestVerifyUnreadableArchive(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	key := storage.ArchiveKey("full", replicasetA)
	f.store.readErrs[key] = errors.New("connection reset by peer")

	report := f.verify()

	issue := findIssue(t, report, IssueUnreadableArchive)
	require.Equal(t, key, issue.Archive)
	require.Contains(t, issue.Detail, "connection reset by peer")
}

func TestVerifyBrokenChain(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	// The parent of the increment is not in the storage: an orphan tail.
	f.addBackup("orphan", "vanished", "full", backup.BackupTypeIncremental, 10, 20)

	report := f.verify()

	require.Equal(t, []IssueKind{IssueChainOrphan}, issueKinds(report))

	issue := findIssue(t, report, IssueChainOrphan)
	require.Equal(t, "orphan", issue.BackupID)
	require.Contains(t, issue.Detail, "vanished")
	require.False(t, issue.Inherited)
}

func TestVerifyVclockMismatch(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	// vclock_begin of the increment does not continue vclock_end of the full.
	f.addBackup("incremental", "full", "full", backup.BackupTypeIncremental, 15, 20)

	report := f.verify()

	issue := findIssue(t, report, IssueVclockMismatch)
	require.Equal(t, "incremental", issue.BackupID)
	require.Contains(t, issue.Detail, replicasetA)
}

func TestVerifyChainFork(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	f.addBackup("inc-a", "full", "full", backup.BackupTypeIncremental, 10, 20)
	f.addBackup("inc-b", "full", "full", backup.BackupTypeIncremental, 10, 20)

	report := f.verify()

	forks := make([]Issue, 0, 2)
	for _, issue := range report.Issues {
		if issue.Kind == IssueChainFork {
			forks = append(forks, issue)
		}
	}

	require.Len(t, forks, 2)
	require.Equal(t, "inc-a", forks[0].BackupID)
	require.Equal(t, "inc-b", forks[1].BackupID)
}

func TestVerifyReportsInheritedChainProblems(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	f.addBackup("orphan", "vanished", "full", backup.BackupTypeIncremental, 10, 20)
	f.addBackup("tail", "orphan", "full", backup.BackupTypeIncremental, 20, 30)

	report := f.verify()

	inherited := make([]Issue, 0, 1)
	for _, issue := range report.Issues {
		if issue.Inherited {
			inherited = append(inherited, issue)
		}
	}

	require.Len(t, inherited, 1)
	require.Equal(t, IssueChainOrphan, inherited[0].Kind)
	require.Equal(t, "tail", inherited[0].BackupID)
}

func TestVerifyInvalidManifest(t *testing.T) {
	f := newFixture(t)
	manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	// A shard outside the declared topology fails manifest validation.
	manifest.Shards[replicasetB] = manifest.Shards[replicasetA]
	f.putManifest(manifest)

	report := f.verify()

	issue := findIssue(t, report, IssueInvalidManifest)
	require.Equal(t, "full", issue.BackupID)
	require.Contains(t, issue.Detail, replicasetB)
}

func TestVerifyDanglingArchive(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	dangling := storage.ArchiveKey("2026-01-01-vanished", replicasetA)
	f.putObject(dangling, []byte("nobody refers to me"))

	report := f.verify()

	issue := findIssue(t, report, IssueDanglingArchive)
	require.Equal(t, dangling, issue.Archive)
	require.Empty(t, issue.BackupID)
	require.Contains(t, issue.Detail, "not referenced")
}

func TestVerifySkipsShardsWithoutArtifact(t *testing.T) {
	f := newFixture(t)
	manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	// A replicaset that failed to back up carries an error, not an artifact.
	manifest.Topology.Replicasets[replicasetB] = []backup.TopologyInstance{{
		InstanceUUID: masterB,
		InstanceName: masterB,
		Hostname:     "localhost",
	}}
	manifest.Shards[replicasetB] = backup.Shard{Error: "instance unreachable"}
	manifest.Status = backup.StatusDegraded
	f.putManifest(manifest)

	report := f.verify()

	require.True(t, report.OK())
	require.Equal(t, 1, report.Archives)
}

func TestVerifyReportsEveryProblemClassAtOnce(t *testing.T) {
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	// Checksum mismatch on the full backup.
	f.putObject(storage.ArchiveKey("full", replicasetA), []byte("corrupted"))
	// Missing archive and a vclock gap on the increment.
	f.addBackup("incremental", "full", "full", backup.BackupTypeIncremental, 15, 20)
	delete(f.store.objects, storage.ArchiveKey("incremental", replicasetA))
	// A chain break and a dangling archive on top.
	f.addBackup("orphan", "vanished", "full", backup.BackupTypeIncremental, 20, 30)
	f.putObject(storage.ArchiveKey("2026-01-01-vanished", replicasetA), []byte("dangling"))

	report := f.verify()

	require.Equal(t, []IssueKind{
		IssueChecksumMismatch,
		IssueVclockMismatch,
		IssueMissingArchive,
		IssueChainOrphan,
		IssueDanglingArchive,
	}, issueKinds(report))
	require.Equal(t, 3, report.Manifests)
	require.Equal(t, 3, report.Archives)
}

func TestVerifyFailsOnStorageError(t *testing.T) {
	f := newFixture(t)
	f.store.listErr = errors.New("storage is unreachable")

	_, err := Verify(t.Context(), f.store)

	require.ErrorContains(t, err, "storage is unreachable")
}

func TestVerifyReportsUndecodableManifest(t *testing.T) {
	// One broken object must not blind the check: the rest is still verified.
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	f.putObject(storage.ManifestKey("broken"), []byte("{"))
	f.putObject(storage.ArchiveKey("full", replicasetA), []byte("corrupted"))

	report := f.verify()

	require.Equal(t,
		[]IssueKind{IssueUnreadableManifest, IssueChecksumMismatch},
		issueKinds(report),
	)
	require.Equal(t, "broken", report.Issues[0].BackupID)
	require.Contains(t, report.Issues[0].Detail, "decode manifest")
	require.Equal(t, 2, report.Manifests)
	require.Equal(t, 1, report.Archives)
}

func TestVerifyNamesObjectsItCannotIdentify(t *testing.T) {
	// Every issue has to point at something an operator can go and look at. A
	// manifest with no backup_id and a stray object under manifests/ both used to
	// produce a row with no backup id, no archive and no key: "something here is
	// broken", with no way to find out what.
	f := newFixture(t)
	unnamed := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	unnamed.BackupID = ""
	f.putObject(storage.ManifestKey("unnamed"), mustJSON(t, unnamed))
	f.putObject(storage.ManifestsPrefix()+"README.txt", []byte("not a manifest"))

	report := f.verify()

	require.Len(t, report.Issues, 2)
	for _, issue := range report.Issues {
		require.Equal(t, IssueUnreadableManifest, issue.Kind)
		require.NotEmpty(t, issue.Manifest, "issue must name the object: %+v", issue)
	}
	require.Equal(t, storage.ManifestsPrefix()+"README.txt", report.Issues[0].Manifest)
	require.Equal(t, storage.ManifestKey("unnamed"), report.Issues[1].Manifest)
}

func TestVerifyStopsWhenRunIsCutShort(t *testing.T) {
	// An expired --timeout means the run could not finish, not that the storage
	// is corrupt. Left as an unreadable_archive issue it would exit 2 "problems
	// found", and every archive after it would go unread and be reported dangling
	// on top - a healthy storage condemned because the clock ran out.
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	f.store.readErrs[storage.ArchiveKey("full", replicasetA)] = context.DeadlineExceeded

	_, err := Verify(t.Context(), f.store)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestVerifyReportsManifestWithoutShards(t *testing.T) {
	// A manifest carrying no shards refers to nothing, so before it was rejected
	// by validation it read as perfectly healthy while its archive looked like
	// garbage - the storage was reported as needing a deletion instead of a fix.
	f := newFixture(t)
	manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	manifest.Shards = map[string]backup.Shard{}
	f.putManifest(manifest)

	report := f.verify()

	require.Contains(t, issueKinds(report), IssueInvalidManifest)
	require.Contains(t, report.Issues[0].Detail, "shards is empty")
}

func TestVerifyKeepsArchivesOfUndecodableManifest(t *testing.T) {
	// The manifest is unreadable, so nothing can say whether it still refers to
	// its archive. Reporting that archive as dangling would hand gc - which acts
	// on exactly this issue - a live backup to delete. An archive belonging to no
	// manifest at all is still reported, so the check is not simply switched off.
	f := newFixture(t)
	f.putObject(storage.ManifestKey("broken"), []byte("{"))
	f.putObject(storage.ArchiveKey("broken", replicasetA), []byte("payload"))
	f.putObject(storage.ArchiveKey("nobody", replicasetB), []byte("garbage"))

	report := f.verify()

	require.Equal(t,
		[]IssueKind{IssueUnreadableManifest, IssueDanglingArchive},
		issueKinds(report),
	)
	require.Equal(t,
		storage.ArchiveKey("nobody", replicasetB),
		report.Issues[1].Archive,
	)
}
