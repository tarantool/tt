package chain

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
)

func TestBuildEmpty(t *testing.T) {
	chain, err := Build(nil)
	require.NoError(t, err)
	require.Empty(t, chain.Groups())
	require.Nil(t, chain.Latest())
	require.Empty(t, chain.Manifests())
	require.Empty(t, chain.Problems())
}

func TestBuildGroupsOldestToNewest(t *testing.T) {
	fullA := manifestFixture("full-a", "", "full-a",
		backup.BackupTypeFull, 10, 0, 10, nil)
	incA := manifestFixture("inc-a", "full-a", "full-a",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	fullB := manifestFixture("full-b", "", "full-b",
		backup.BackupTypeFull, 30, 0, 10, nil)
	incB := manifestFixture("inc-b", "full-b", "full-b",
		backup.BackupTypeIncremental, 40, 10, 20, nil)

	chain := buildFixtureChain(t, incA, fullB, incB, fullA)
	groups := chain.Groups()
	require.Len(t, groups, 2)
	require.Equal(t, []backup.BackupID{"full-a", "inc-a"}, entryIDs(groups[0].Entries))
	require.Equal(t, []backup.BackupID{"full-b", "inc-b"}, entryIDs(groups[1].Entries))
	require.Empty(t, chain.Problems())
}

func TestBuildKeepsBrokenTailInDeclaredGroup(t *testing.T) {
	fullA := manifestFixture("full-a", "", "full-a", backup.BackupTypeFull, 10, 0, 10, nil)
	incA := manifestFixture("inc-a", "full-a", "full-a",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	brokenA := manifestFixture("broken-a", "missing", "full-a",
		backup.BackupTypeIncremental, 25, 20, 30, nil)
	fullB := manifestFixture("full-b", "", "full-b",
		backup.BackupTypeFull, 30, 0, 10, nil)
	incB := manifestFixture("inc-b", "full-b", "full-b",
		backup.BackupTypeIncremental, 40, 10, 20, nil)

	chain := buildFixtureChain(t, incB, brokenA, fullA, fullB, incA)
	groups := chain.Groups()
	require.Equal(t, []backup.BackupID{"full-a", "inc-a", "broken-a"}, entryIDs(groups[0].Entries))
	require.Equal(t, []backup.BackupID{"full-b", "inc-b"}, entryIDs(groups[1].Entries))

	broken := entriesByID(groups[0].Entries)["broken-a"]
	requireProblems(t, broken, problemExpectation{
		kind:           ProblemOrphan,
		backupID:       "broken-a",
		detailContains: []string{"missing"},
	})
}

func TestBuildLatestUsesNewestGroup(t *testing.T) {
	fullA := manifestFixture("full-a", "", "full-a",
		backup.BackupTypeFull, 10, 0, 10, nil)
	incA := manifestFixture("inc-a", "full-a", "full-a",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	fullB := manifestFixture("full-b", "", "full-b",
		backup.BackupTypeFull, 30, 0, 10, nil)
	incB := manifestFixture("inc-b", "full-b", "full-b",
		backup.BackupTypeIncremental, 40, 10, 20, nil)

	chain := buildFixtureChain(t, incB, fullA, incA, fullB)
	require.Equal(t, backup.BackupID("inc-b"), chain.Latest().Manifest.BackupID)
}

func TestBuildLatestReturnsProblematicHead(t *testing.T) {
	// The chain head is a problematic entry.
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	clean := manifestFixture("inc", "full", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	orphanHead := manifestFixture("orphan", "missing", "orphan",
		backup.BackupTypeFull, 30, 0, 30, nil)

	chain := buildFixtureChain(t, full, clean, orphanHead)
	latest := chain.Latest()
	require.Equal(t, backup.BackupID("orphan"), latest.Manifest.BackupID)
	require.NotEmpty(t, latest.Problems)
}

func TestBuildOrderFollowsBackupIDNotCreationTime(t *testing.T) {
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 100, 0, 10, nil)
	// inc-z sorts last by backup_id but was created earliest (CreationTime=50).
	head := manifestFixture("inc-z", "full", "full",
		backup.BackupTypeIncremental, 50, 10, 20, nil)

	chain := buildFixtureChain(t, head, full)
	require.Equal(t, backup.BackupID("inc-z"), chain.Latest().Manifest.BackupID)
	require.Equal(t,
		[]backup.BackupID{"full", "inc-z"},
		entryIDs(chain.Groups()[0].Entries),
	)
}

func TestBuildRejectsDuplicateBackupID(t *testing.T) {
	first := manifestFixture("duplicate", "", "duplicate",
		backup.BackupTypeFull, 10, 0, 10, nil)
	second := manifestFixture("duplicate", "", "other",
		backup.BackupTypeFull, 20, 0, 10, nil)

	_, err := Build([]*backup.ClusterManifest{&first, &second})
	require.ErrorContains(t, err, `duplicate backup_id "duplicate"`)
}

func TestBuildMarksOrphanAndDescendants(t *testing.T) {
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	orphan := manifestFixture("orphan", "missing", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	child := manifestFixture("child", "orphan", "full",
		backup.BackupTypeIncremental, 30, 20, 30, nil)

	chain := buildFixtureChain(t, orphan, child, full)
	entries := entriesByID(chain.Groups()[0].Entries)
	require.Empty(t, entries["full"].Problems)
	requireProblems(t, entries["orphan"], problemExpectation{
		kind:           ProblemOrphan,
		backupID:       "orphan",
		detailContains: []string{"missing"},
	})
	requireProblems(t, entries["child"], problemExpectation{
		kind:           ProblemOrphan,
		backupID:       "child",
		inherited:      true,
		detailContains: []string{"missing"},
	})

	problems := chain.Problems()
	require.Len(t, problems, 2)
	require.Equal(t, entries["orphan"].Problems[0], problems[0])
	require.Equal(t, entries["child"].Problems[0], problems[1])
}

func TestBuildMarksBothForkTails(t *testing.T) {
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	first := manifestFixture("first", "full", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	second := manifestFixture("second", "full", "full",
		backup.BackupTypeIncremental, 21, 10, 20, nil)
	firstChild := manifestFixture("first-child", "first", "full",
		backup.BackupTypeIncremental, 30, 20, 30, nil)
	secondChild := manifestFixture("second-child", "second", "full",
		backup.BackupTypeIncremental, 31, 20, 30, nil)

	chain := buildFixtureChain(t, secondChild, first, full, firstChild, second)
	entries := entriesByID(chain.Groups()[0].Entries)
	require.Empty(t, entries["full"].Problems)
	for _, id := range []string{"first", "second"} {
		requireProblems(t, entries[backup.BackupID(id)], problemExpectation{
			kind:           ProblemFork,
			backupID:       id,
			detailContains: []string{"full", "first", "second"},
		})
	}
	for _, id := range []string{"first-child", "second-child"} {
		requireProblems(t, entries[backup.BackupID(id)], problemExpectation{
			kind:           ProblemFork,
			backupID:       id,
			inherited:      true,
			detailContains: []string{"full", "first", "second"},
		})
	}
}

func TestBuildMarksNonMonotonicForkTails(t *testing.T) {
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	head := manifestFixture("inc-b", "full", "full",
		backup.BackupTypeIncremental, 30, 10, 20, nil)
	stale := manifestFixture("inc-a", "full", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)

	chain := buildFixtureChain(t, full, head, stale)
	entries := entriesByID(chain.Groups()[0].Entries)
	require.Empty(t, entries["full"].Problems)
	for _, id := range []string{"inc-a", "inc-b"} {
		requireProblems(t, entries[backup.BackupID(id)], problemExpectation{
			kind:           ProblemFork,
			backupID:       id,
			detailContains: []string{"full", "inc-a", "inc-b"},
		})
	}
}

func TestBuildMarksVclockMismatchOnOneShardAndDescendant(t *testing.T) {
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	broken := manifestFixture("broken", "full", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	broken.Shards[replicasetB].Instance.VclockBegin = backup.Vclock{2: 9}
	child := manifestFixture("child", "broken", "full",
		backup.BackupTypeIncremental, 30, 20, 30, nil)

	chain := buildFixtureChain(t, full, broken, child)
	entries := entriesByID(chain.Groups()[0].Entries)
	require.Empty(t, entries["full"].Problems)
	requireProblems(t, entries["broken"], problemExpectation{
		kind:           ProblemVclockMismatch,
		backupID:       "broken",
		detailContains: []string{replicasetB, "map[2:9]", "map[2:10]"},
	})

	// Only replicasetB is mismatched: the problem detail must not mention replicasetA.
	require.NotContains(t, entries["broken"].Problems[0].Detail, replicasetA)
	requireProblems(t, entries["child"], problemExpectation{
		kind:           ProblemVclockMismatch,
		backupID:       "child",
		inherited:      true,
		detailContains: []string{replicasetB, "map[2:9]", "map[2:10]"},
	})
}

func TestBuildMarksVclockMismatchOnBothShards(t *testing.T) {
	// Both replicasets have mismatched vclock_begin — two separate Problems.
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	broken := manifestFixture("broken", "full", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	broken.Shards[replicasetA].Instance.VclockBegin = backup.Vclock{1: 9}
	broken.Shards[replicasetB].Instance.VclockBegin = backup.Vclock{2: 9}

	chain := buildFixtureChain(t, full, broken)
	problems := entriesByID(chain.Groups()[0].Entries)["broken"].Problems
	require.Len(t, problems, 2)
	require.Equal(t, ProblemVclockMismatch, problems[0].Kind)
	require.Equal(t, ProblemVclockMismatch, problems[1].Kind)
	// Each problem names a different replicaset.
	mentionedReplicasets := []string{problems[0].Detail, problems[1].Detail}
	require.Contains(t, mentionedReplicasets[0]+mentionedReplicasets[1], replicasetA)
	require.Contains(t, mentionedReplicasets[0]+mentionedReplicasets[1], replicasetB)
}

func TestBuildSkipsVclockCheckForFullBackup(t *testing.T) {
	// A full backup's vclock_begin is never compared with the previous manifest:
	// a full is a chain root, not a delta.
	prev := manifestFixture("prev", "", "prev",
		backup.BackupTypeFull, 10, 0, 10, nil)
	// next is declared as a full with an arbitrary vclock_begin that would fail
	// an incremental check against prev's vclock_end.
	next := manifestFixture("next", "prev", "next",
		backup.BackupTypeFull, 20, 0, 20, nil)

	chain := buildFixtureChain(t, prev, next)
	for _, group := range chain.Groups() {
		for _, entry := range group.Entries {
			require.Empty(t, entry.Problems)
		}
	}
}

func TestBuildManifestsIncludesProblematic(t *testing.T) {
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	orphan := manifestFixture("orphan", "missing", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)

	chain := buildFixtureChain(t, full, orphan)
	require.Equal(t,
		[]backup.BackupID{"full", "orphan"},
		manifestIDs(chain.Manifests()),
	)
	require.Len(t, chain.Problems(), 1)
}

func TestBuildMarksInvalidManifestAndDescendant(t *testing.T) {
	full := manifestFixture("full", "", "full",
		backup.BackupTypeFull, 10, 0, 10, nil)
	invalid := manifestFixture("invalid", "full", "full",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	invalid.SchemaVersion = 0
	child := manifestFixture("child", "invalid", "full",
		backup.BackupTypeIncremental, 30, 20, 30, nil)

	chain := buildFixtureChain(t, full, invalid, child)
	entries := entriesByID(chain.Groups()[0].Entries)
	require.Empty(t, entries["full"].Problems)
	requireProblems(t, entries["invalid"], problemExpectation{
		kind:           ProblemInvalidManifest,
		backupID:       "invalid",
		detailContains: []string{"schema_version"},
	})
	requireProblems(t, entries["child"], problemExpectation{
		kind:           ProblemInvalidManifest,
		backupID:       "child",
		inherited:      true,
		detailContains: []string{"schema_version"},
	})
}

func TestBuildStitchesShardAcrossHoleByLastCarryingManifest(t *testing.T) {
	// M2 is a hole for replicasetB (shard error). M3's replicasetB vclock_begin
	// must be stitched against M1 (the last manifest carrying the shard), not
	// against the hole in M2, and must not raise a false mismatch.
	m1 := manifestFixture("m1", "", "m1",
		backup.BackupTypeFull, 10, 0, 10, nil)
	m2 := manifestFixture("m2", "m1", "m1",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	makeShardHole(&m2)
	m3 := manifestFixture("m3", "m2", "m1",
		backup.BackupTypeIncremental, 30, 20, 30, nil)
	// replicasetB in M3 continues from M1's vclock_end (10), across the M2 hole.
	m3.Shards[replicasetB].Instance.VclockBegin = backup.Vclock{2: 10}

	chain := buildFixtureChain(t, m1, m2, m3)
	entries := entriesByID(chain.Groups()[0].Entries)
	// replicasetA is continuous through all three; replicasetB stitches over the
	// hole. Neither must be flagged.
	require.Empty(t, entries["m3"].Problems)
}

func TestBuildDetectsRealMismatchAcrossHole(t *testing.T) {
	// Same shape, but M3's replicasetB does not line up with M1's vclock_end:
	// stitching across the hole must catch the real mismatch.
	m1 := manifestFixture("m1", "", "m1",
		backup.BackupTypeFull, 10, 0, 10, nil)
	m2 := manifestFixture("m2", "m1", "m1",
		backup.BackupTypeIncremental, 20, 10, 20, nil)
	makeShardHole(&m2)
	m3 := manifestFixture("m3", "m2", "m1",
		backup.BackupTypeIncremental, 30, 20, 30, nil)
	// replicasetB in M3 claims to continue from 99, which matches neither M1 nor M2.
	m3.Shards[replicasetB].Instance.VclockBegin = backup.Vclock{2: 99}

	chain := buildFixtureChain(t, m1, m2, m3)
	entries := entriesByID(chain.Groups()[0].Entries)
	requireProblems(t, entries["m3"], problemExpectation{
		kind:           ProblemVclockMismatch,
		backupID:       "m3",
		detailContains: []string{replicasetB, "m1"},
	})
}

func TestBuildHandlesCycleInPreviousBackupID(t *testing.T) {
	// A cycle in previous_backup_id must not hang Build. Because both entries
	// resolve their previous_backup_id to an existing entry, neither is marked
	// as an orphan. The cycle itself is not currently detected as a Problem.
	a := manifestFixture("a", "b", "a",
		backup.BackupTypeFull, 10, 0, 10, nil)
	b := manifestFixture("b", "a", "a",
		backup.BackupTypeIncremental, 20, 10, 20, nil)

	chain := buildFixtureChain(t, a, b)
	require.Len(t, chain.Manifests(), 2)
}

type problemExpectation struct {
	kind           ProblemKind
	backupID       string
	inherited      bool
	detailContains []string
}

func requireProblems(t *testing.T, entry *Entry, expected ...problemExpectation) {
	t.Helper()
	require.Len(t, entry.Problems, len(expected))
	for i, want := range expected {
		problem := entry.Problems[i]
		require.Equal(t, want.kind, problem.Kind)
		require.Equal(t, want.backupID, problem.BackupID)
		require.Equal(t, want.inherited, problem.Inherited)
		for _, part := range want.detailContains {
			require.Contains(t, problem.Detail, part)
		}
	}
}
