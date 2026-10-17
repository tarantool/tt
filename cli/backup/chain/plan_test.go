package chain

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
)

func TestPlanForFullOnly(t *testing.T) {
	// Point inside a full: plan is the full alone, TrimTo set.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 20,
		clusterPointFixture("target", 15, 15))
	chain := buildFixtureChain(t, full)

	plan, err := chain.PlanFor(findPoint(t, chain, "target"))
	require.NoError(t, err)
	require.Equal(t, "target", plan.Point.Name)
	require.Equal(t,
		[]backup.BackupID{"full"},
		manifestIDs(plan.Shards[replicasetA].Backups),
	)
	require.Equal(t, &Position{ReplicaID: 1, LSN: 15}, plan.Shards[replicasetA].TrimTo)
	require.Equal(t, &Position{ReplicaID: 2, LSN: 15}, plan.Shards[replicasetB].TrimTo)
}

func TestPlanForFullAndIncrementals(t *testing.T) {
	// Point inside an incremental: plan is full + incremental, TrimTo on the last.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	incremental := manifestFixture("inc", "full", "full", backup.BackupTypeIncremental, 20, 10, 20,
		clusterPointFixture("target", 18, 18))
	chain := buildFixtureChain(t, full, incremental)

	plan, err := chain.PlanFor(findPoint(t, chain, "target"))
	require.NoError(t, err)
	require.Equal(t, "target", plan.Point.Name)
	require.Equal(t,
		[]backup.BackupID{"full", "inc"},
		manifestIDs(plan.Shards[replicasetA].Backups),
	)
	require.Equal(t, &Position{ReplicaID: 1, LSN: 18}, plan.Shards[replicasetA].TrimTo)
	require.Equal(t,
		[]backup.BackupID{"full", "inc"},
		manifestIDs(plan.Shards[replicasetB].Backups),
	)
	require.Equal(t, &Position{ReplicaID: 2, LSN: 18}, plan.Shards[replicasetB].TrimTo)
}

func TestPlanForTrimToNilWhenPointAtManifestEnd(t *testing.T) {
	// Point at vclock_end exactly: no trimming, TrimTo nil.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 20,
		clusterPointFixture("target", 20, 15))
	chain := buildFixtureChain(t, full)

	plan, err := chain.PlanFor(findPoint(t, chain, "target"))
	require.NoError(t, err)
	require.Nil(t, plan.Shards[replicasetA].TrimTo)
	require.Nil(t, plan.Shards[replicasetB].TrimTo)
}

func TestPlanForSkipsShardHole(t *testing.T) {
	// M2 is a hole for replicasetB: its plan skips M2, shorter than replicasetA's.
	m1 := manifestFixture("m1", "", "m1", backup.BackupTypeFull, 10, 0, 10, nil)
	m2 := manifestFixture("m2", "m1", "m1", backup.BackupTypeIncremental, 20, 10, 20, nil)
	makeShardHole(&m2)
	m3 := manifestFixture("m3", "m2", "m1", backup.BackupTypeIncremental, 30, 20, 30,
		clusterPointFixture("target", 25, 25))
	m3.Shards[replicasetB].Instance.VclockBegin = backup.Vclock{2: 10}

	chain := buildFixtureChain(t, m1, m2, m3)
	plan, err := chain.PlanFor(findPoint(t, chain, "target"))
	require.NoError(t, err)

	require.Equal(t,
		[]backup.BackupID{"m1", "m2", "m3"},
		manifestIDs(plan.Shards[replicasetA].Backups),
	)
	require.Equal(t,
		[]backup.BackupID{"m1", "m3"},
		manifestIDs(plan.Shards[replicasetB].Backups),
	)
}

func TestPlanForRejectsForeignPoint(t *testing.T) {
	chain := buildFixtureChain(t, manifestFixture(
		"full", "", "full", backup.BackupTypeFull, 10, 0, 10,
		clusterPointFixture("known", 8, 8),
	))

	_, err := chain.PlanFor(ClusterPoint{Name: "foreign"})
	require.ErrorContains(t, err, "foreign")
}

// A label is not a key: the core creates two recovery points with the same
// label without complaint, so a chain can carry one twice. The position picks
// the backup, and the answer must not depend on map iteration order -- the
// entry chosen decides what gets trimmed, so picking the wrong one restores an
// instance that looks healthy at the wrong state.
func TestPlanForPicksTheBackupHoldingThePositionWhenALabelRepeats(t *testing.T) {
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 100,
		clusterPointFixture("dup", 50, 50))
	incremental := manifestFixture("inc", "full", "full",
		backup.BackupTypeIncremental, 20, 100, 200,
		clusterPointFixture("dup", 150, 150))
	chain := buildFixtureChain(t, full, incremental)

	// Ask for the position that lives in the increment, by the shared label.
	point := ClusterPoint{Name: "dup", Shards: map[string]Position{
		replicasetA: {ReplicaID: 1, LSN: 150},
		replicasetB: {ReplicaID: 2, LSN: 150},
	}}

	// Repeated so a map-order regression cannot pass by luck.
	for range 64 {
		plan, err := chain.PlanFor(point)
		require.NoError(t, err)
		require.Equal(t,
			[]backup.BackupID{"full", "inc"},
			manifestIDs(plan.Shards[replicasetA].Backups),
		)
		require.Equal(t, &Position{ReplicaID: 1, LSN: 150},
			plan.Shards[replicasetA].TrimTo)
	}
}

// The first backup of every chain has no begin vclock -- the core reports none
// for a full backup -- so a lower bound must not reject it.
func TestPlanForMatchesAFullBackupThatHasNoBeginVclock(t *testing.T) {
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 100,
		clusterPointFixture("target", 40, 40))
	chain := buildFixtureChain(t, full)

	plan, err := chain.PlanFor(findPoint(t, chain, "target"))
	require.NoError(t, err)
	require.Equal(t, []backup.BackupID{"full"},
		manifestIDs(plan.Shards[replicasetA].Backups))
	require.Equal(t, &Position{ReplicaID: 1, LSN: 40}, plan.Shards[replicasetA].TrimTo)
}

// A backup covering the position while carrying no such label means the
// manifests disagree with each other. Restoring on either reading is a guess,
// so the plan says so instead.
func TestPlanForReportsAManifestSetThatDisagreesWithItself(t *testing.T) {
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 100,
		clusterPointFixture("early", 50, 50))
	incremental := manifestFixture("inc", "full", "full",
		backup.BackupTypeIncremental, 20, 100, 200,
		clusterPointFixture("late", 150, 150))
	chain := buildFixtureChain(t, full, incremental)

	// "late" is a point of the increment, but the position given for it sits
	// back inside the full backup: the two cannot both be true.
	_, err := chain.PlanFor(ClusterPoint{Name: "late", Shards: map[string]Position{
		replicasetA: {ReplicaID: 1, LSN: 50},
		replicasetB: {ReplicaID: 2, LSN: 50},
	}})

	require.ErrorContains(t, err, "late")
	require.ErrorContains(t, err, "full")
}

// The core lists in a full backup only the points past its checkpoint, while a
// full is bounded only by its end vclock. A full of another chain whose
// checkpoint is already past a point therefore covers that point's position
// without carrying it. The plan must look only at the chain the point was
// stitched from, however the backup ids of the two chains sort.
func TestPlanForIgnoresAFullOfAnotherChainCoveringThePosition(t *testing.T) {
	for _, ids := range [][3]string{
		{"z-full", "z-inc", "a-full"},
		{"a-full", "a-inc", "z-full"},
	} {
		t.Run(ids[0], func(t *testing.T) {
			full := manifestFixture(ids[0], "", ids[0], backup.BackupTypeFull,
				10, 0, 100, nil)
			incremental := manifestFixture(ids[1], ids[0], ids[0],
				backup.BackupTypeIncremental, 20, 100, 200,
				clusterPointFixture("target", 150, 20))
			other := manifestFixture(ids[2], "", ids[2], backup.BackupTypeFull,
				30, 0, 300, nil)
			chain := buildFixtureChain(t, full, incremental, other)

			plan, err := chain.PlanFor(findPoint(t, chain, "target"))
			require.NoError(t, err)
			for _, replicasetUUID := range []string{replicasetA, replicasetB} {
				require.Equal(t,
					[]backup.BackupID{
						backup.BackupID(ids[0]), backup.BackupID(ids[1]),
					},
					manifestIDs(plan.Shards[replicasetUUID].Backups),
				)
			}
		})
	}
}

// A backup covering the position and carrying the label, but at another
// position, holds no such point either: trimming inside it would land on a
// state no point describes.
func TestPlanForReportsALabelCarriedAtAnotherPosition(t *testing.T) {
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 100,
		clusterPointFixture("target", 50, 50))
	chain := buildFixtureChain(t, full)

	_, err := chain.PlanFor(ClusterPoint{Name: "target", Shards: map[string]Position{
		replicasetA: {ReplicaID: 1, LSN: 60},
		replicasetB: {ReplicaID: 2, LSN: 60},
	}})

	require.ErrorContains(t, err, "target")
	require.ErrorContains(t, err, "full")
}
