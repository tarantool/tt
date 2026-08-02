package verify

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
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
	require.False(t, issue.Informational)
	require.False(t, report.OK())
}

func TestVerifyReportsAnUploadInProgressWithoutFailing(t *testing.T) {
	// An upload writes its archives before the manifest that references them, so
	// a verify running during the nightly backup sees unreferenced archives of a
	// backup that is perfectly fine. Failing here would fire an alert on every
	// backup, and an alert that always fires is one nobody reads.
	f := newFixture(t)
	f.addBackup("2026-01-01", "", "2026-01-01", backup.BackupTypeFull, 0, 10)
	uploading := storage.ArchiveKey("2026-01-02", replicasetA)
	f.putObject(uploading, []byte("still uploading"))

	report := f.verify()

	require.True(t, report.OK())
	require.Zero(t, report.Problems())

	issue := findIssue(t, report, IssueUploadInProgress)
	require.Equal(t, uploading, issue.Archive)
	require.Equal(t, "2026-01-02", issue.BackupID)
	require.True(t, issue.Informational)
	require.Contains(t, issue.Detail, "upload may still be in progress")
}

func TestVerifyReportsTheFirstUploadOfAnEmptyStorageAsInProgress(t *testing.T) {
	// Nothing to compare against: a storage holding archives and no manifest at
	// all is a cluster whose first backup has not finished uploading.
	f := newFixture(t)
	uploading := storage.ArchiveKey("2026-01-01", replicasetA)
	f.putObject(uploading, []byte("still uploading"))

	report := f.verify()

	require.True(t, report.OK())
	require.Equal(t, []IssueKind{IssueUploadInProgress}, issueKinds(report))
}

func TestVerifyStillFailsOnAnArchiveOlderThanTheNewestManifest(t *testing.T) {
	// The pipeline has moved past this backup id, so nothing is uploading it any
	// more: the archive is abandoned and the storage is not healthy.
	f := newFixture(t)
	f.addBackup("2026-01-02", "", "2026-01-02", backup.BackupTypeFull, 0, 10)
	abandoned := storage.ArchiveKey("2026-01-01", replicasetA)
	f.putObject(abandoned, []byte("abandoned"))

	report := f.verify()

	require.False(t, report.OK())
	require.Equal(t, 1, report.Problems())
	require.Equal(t, []IssueKind{IssueDanglingArchive}, issueKinds(report))
}

func TestVerifyCountsOnlyRealProblems(t *testing.T) {
	f := newFixture(t)
	f.addBackup("2026-01-01", "", "2026-01-01", backup.BackupTypeFull, 0, 10)
	// One real defect and one upload in progress: the verdict follows the defect,
	// and the count reports only it.
	f.putObject(storage.ArchiveKey("2026-01-01", replicasetA), []byte("corrupted"))
	f.putObject(storage.ArchiveKey("2026-01-02", replicasetA), []byte("uploading"))

	report := f.verify()

	require.False(t, report.OK())
	require.Equal(t, 1, report.Problems())
	require.Len(t, report.Issues, 2)
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

func TestVerifyStopsWhenAManifestReadIsCutShort(t *testing.T) {
	// This is where the deadline of a real run lands: the listing comes back in
	// one call, then the manifests are read one at a time. Turned into findings,
	// an expired --timeout would exit 2 with "your backups are corrupt" - one
	// such issue for every manifest the clock did not reach.
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	f.addBackup("incremental", "full", "full", backup.BackupTypeIncremental, 10, 20)
	f.store.readErrs[storage.ManifestKey("full")] = context.DeadlineExceeded

	report, err := Verify(t.Context(), f.store)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, storage.ManifestKey("full"))
	require.Nil(t, report, "a run that was cut short must not produce a verdict")
}

func TestVerifyFailsWhenTheArchiveListingFails(t *testing.T) {
	// The manifests were read, then the storage stopped answering. A run that
	// never listed data/ knows nothing about what is stored there, so it must not
	// answer at all: handing back the manifest-side issues would let a
	// half-scanned storage pass for a complete verdict.
	f := newFixture(t)
	f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	f.putObject(storage.ArchiveKey("full", replicasetA), []byte("corrupted"))
	f.store.listErrs[storage.DataPrefix()] = errors.New("permission denied")

	report, err := Verify(t.Context(), f.store)

	require.ErrorContains(t, err, "failed to check for dangling archives")
	require.ErrorContains(t, err, "permission denied")
	require.Nil(t, report, "a storage that could not be listed has no verdict")
}

// The in-flight/dangling split is one lexicographic comparison of two
// operator-supplied strings: no clock, no LastModified, no age window. Backup
// ids come from a free-form --backup-id flag nothing validates, so a site
// numbering its backups without padding gets the opposite answer - "backup-10"
// sorts below "backup-9", and an upload that is still running reads as
// abandoned garbage that gc is then free to collect. Both rows below are
// today's behaviour; a fix that consults LastModified when the id does not
// order has to change the second one deliberately.
func TestVerifyClassifiesInFlightArchiveByBackupIDOrder(t *testing.T) {
	tests := []struct {
		name      string
		stored    string
		uploading string
		want      IssueKind
		healthy   bool
	}{
		{
			name:      "ids that sort in creation order",
			stored:    "2026-01-01",
			uploading: "2026-01-02",
			want:      IssueUploadInProgress,
			healthy:   true,
		},
		{
			name:      "ids that do not",
			stored:    "backup-9",
			uploading: "backup-10",
			want:      IssueDanglingArchive,
			healthy:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.addBackup(test.stored, "", test.stored, backup.BackupTypeFull, 0, 10)
			// An upload writes its archives before its manifest, so this is what
			// any upload in progress looks like from the outside.
			uploading := storage.ArchiveKey(test.uploading, replicasetA)
			f.putObject(uploading, []byte("still uploading"))

			report := f.verify()

			require.Equal(t, []IssueKind{test.want}, issueKinds(report))
			require.Equal(t, uploading, findIssue(t, report, test.want).Archive)
			require.Equal(t, test.healthy, report.OK())
		})
	}
}

func TestVerifyRejectsATraversingArtifactPath(t *testing.T) {
	// artifact.path is content: whoever writes a manifest chooses it, and the
	// file backend turns a key into root + key. storage.CleanKey is the whole
	// guard, so every key verify hands to the backend has to stay inside the
	// root - a manifest must never make verify read /etc/passwd.
	tests := []struct {
		name    string
		path    string
		archive string
		detail  string
	}{
		{
			name:    "relative traversal",
			path:    "../../etc/passwd",
			archive: "../../etc/passwd",
			detail:  "not a storage key",
		},
		{
			name:    "traversal out of data/",
			path:    "data/../../x",
			archive: "data/../../x",
			detail:  "not a storage key",
		},
		{
			name:    "backslash separator",
			path:    `a\b`,
			archive: `a\b`,
			detail:  "not a storage key",
		},
		{
			// Not rejected, normalised: the leading slash is stripped and the key
			// is looked up inside the storage, where there is no such object.
			name:    "absolute path is read inside the root",
			path:    "/etc/hosts",
			archive: "etc/hosts",
			detail:  "archive is not present in the storage",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
			manifest.Shards[replicasetA].Instance.Artifact.Path = test.path
			f.putManifest(manifest)

			report := f.verify()

			issue := findIssue(t, report, IssueMissingArchive)
			require.Equal(t, test.archive, issue.Archive)
			require.Contains(t, issue.Detail, test.detail)

			require.NotEmpty(t, f.store.gets, "verify read nothing at all")
			for _, key := range f.store.gets {
				require.NotContains(t, key, "..",
					"verify asked the storage for a key outside it: %q", key)
				require.False(t, strings.HasPrefix(key, "/"),
					"verify asked the storage for an absolute path: %q", key)
			}
		})
	}
}

func TestVerifyReportsAnArtifactPathOutsideData(t *testing.T) {
	// A key only has to be in-root, not under data/, so a manifest may point at
	// any object - here at another manifest. Verify then checksums that object as
	// if it were an archive and calls the real archive garbage. This is today's
	// report, not the honest one: "artifact path is not under data/" deserves an
	// issue class of its own, and adding it has to change this test.
	f := newFixture(t)
	manifest := f.addBackup("full", "", "full", backup.BackupTypeFull, 0, 10)
	manifest.Shards[replicasetA].Instance.Artifact.Path = storage.ManifestKey("full")
	f.putManifest(manifest)

	report := f.verify()

	require.Equal(t,
		[]IssueKind{IssueChecksumMismatch, IssueDanglingArchive},
		issueKinds(report),
	)
	require.Equal(t, storage.ManifestKey("full"),
		findIssue(t, report, IssueChecksumMismatch).Archive)
	require.Equal(t, storage.ArchiveKey("full", replicasetA),
		findIssue(t, report, IssueDanglingArchive).Archive)
}

func TestVerifyReportsTwoManifestsClaimingOneArchive(t *testing.T) {
	// Referenced archives are collected into a set of keys, so two manifests
	// naming one object look exactly like one manifest naming it. What is left
	// over is the second backup's own, live archive - and the report hands the
	// operator, and gc after them, that archive to delete. The aliasing itself,
	// which is the actual defect, is never named.
	f := newFixture(t)
	f.addBackup("2026-01-01-full", "", "2026-01-01-full", backup.BackupTypeFull, 0, 10)
	shared := storage.ArchiveKey("2026-01-01-full", replicasetA)

	second := f.addBackup("2026-01-02-full", "", "2026-01-02-full",
		backup.BackupTypeFull, 0, 10)
	// An orchestrator that copied the whole artifact block, checksum included.
	artifact := &second.Shards[replicasetA].Instance.Artifact
	artifact.Path = shared
	artifact.ChecksumSHA256 = checksumOf(f.store.objects[shared])
	f.putManifest(second)

	report := f.verify()

	require.Equal(t, []IssueKind{IssueDanglingArchive}, issueKinds(report))
	require.Equal(t, storage.ArchiveKey("2026-01-02-full", replicasetA),
		findIssue(t, report, IssueDanglingArchive).Archive)
	// Two shard artifacts were checked; the count cannot see that a single
	// object answered for both of them.
	require.Equal(t, 2, report.Archives)
}

func TestChainIssueKindCoversEveryChainProblemKind(t *testing.T) {
	// Every chain problem kind has an issue kind of its own; the fallback exists
	// for the kind added next. Unmapped, that kind reaches the report as
	// "chain_problem" - a class no output format lists, no help text mentions and
	// no consumer handles, carrying nothing but a detail string. The last row is
	// the value a newly declared kind would take, so declaring one fails this
	// test and makes the mapping a decision instead of an omission.
	tests := []struct {
		name string
		kind chain.ProblemKind
		want IssueKind
	}{
		{"orphan", chain.ProblemOrphan, IssueChainOrphan},
		{"fork", chain.ProblemFork, IssueChainFork},
		{"vclock mismatch", chain.ProblemVclockMismatch, IssueVclockMismatch},
		{"invalid manifest", chain.ProblemInvalidManifest, IssueInvalidManifest},
		{"a kind declared later", chain.ProblemInvalidManifest + 1, IssueChainProblem},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, chainIssueKind(test.kind))
		})
	}
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
