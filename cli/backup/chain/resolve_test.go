package chain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
)

func TestResolveExactPoint(t *testing.T) {
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 30, 0, 30,
		map[string][]backup.RecoveryPoint{
			replicasetA: {recoveryPoint("point", 1, 10, 10)},
			replicasetB: {recoveryPoint("point", 2, 10, 11)},
		})

	resolution := buildFixtureChain(t, manifest).Resolve(time.Unix(10, 0).UTC())
	require.Equal(t, StatusOK, resolution.Status)
	require.Equal(t, "point", resolution.Point.Name)
	require.Nil(t, resolution.Before)
	require.Nil(t, resolution.After)
}

func TestResolveLatestPointNotLaterThanT(t *testing.T) {
	// T between two points resolves to the earlier one ("not later than T").
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 30, 0, 30,
		map[string][]backup.RecoveryPoint{
			replicasetA: {
				recoveryPoint("before", 1, 10, 10),
				recoveryPoint("after", 1, 20, 20),
			},
			replicasetB: {
				recoveryPoint("before", 2, 10, 11),
				recoveryPoint("after", 2, 20, 21),
			},
		})

	resolution := buildFixtureChain(t, manifest).Resolve(time.Unix(15, 0).UTC())
	require.Equal(t, StatusOK, resolution.Status)
	require.Equal(t, "before", resolution.Point.Name)
	require.Nil(t, resolution.Before)
	require.Nil(t, resolution.After)
}

func TestResolveBetweenPoints(t *testing.T) {
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 30, 0, 30,
		map[string][]backup.RecoveryPoint{
			replicasetA: {
				recoveryPoint("before", 1, 10, 10),
				recoveryPoint("after", 1, 20, 20),
			},
			replicasetB: {
				recoveryPoint("before", 2, 10, 11),
				recoveryPoint("after", 2, 20, 21),
			},
		})

	// T=9 is before every point.
	resolution := buildFixtureChain(t, manifest).Resolve(time.Unix(9, 0).UTC())
	require.Equal(t, StatusNoRecoveryPoint, resolution.Status)
	require.Nil(t, resolution.Point)
	require.Equal(t, "before", resolution.After.Name)
	require.Nil(t, resolution.Before)
}

func TestResolveTAfterLastPoint(t *testing.T) {
	// T past all coverage → out_of_range with the last point as a hint.
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 30, 0, 30,
		map[string][]backup.RecoveryPoint{
			replicasetA: {recoveryPoint("point", 1, 10, 10)},
			replicasetB: {recoveryPoint("point", 2, 10, 11)},
		})

	resolution := buildFixtureChain(t, manifest).Resolve(time.Unix(100, 0).UTC())
	require.Equal(t, StatusOutOfRange, resolution.Status)
	require.Nil(t, resolution.Point)
	require.Equal(t, "point", resolution.Before.Name)
	require.Nil(t, resolution.After)
}

func TestResolveEmptyChain(t *testing.T) {
	resolution := buildFixtureChain(t).Resolve(time.Unix(10, 0).UTC())
	require.Equal(t, StatusOutOfRange, resolution.Status)
	require.Nil(t, resolution.Point)
}

func clusterPointFixture(name string, lsn uint64,
	timestamp int64,
) map[string][]backup.RecoveryPoint {
	return map[string][]backup.RecoveryPoint{
		replicasetA: {recoveryPoint(name, 1, lsn, timestamp)},
		replicasetB: {recoveryPoint(name, 2, lsn, timestamp+1)},
	}
}

func TestResolveTopologyBoundaryGap(t *testing.T) {
	// T falls in the gap across a topology boundary: report the boundary and both
	// neighbors, without silently returning the earlier point.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10,
		clusterPointFixture("before", 100, 10))
	incremental := manifestFixture("inc", "full", "full", backup.BackupTypeIncremental, 20, 10, 20,
		clusterPointFixture("after", 200, 30))
	changeMaster(&incremental, replicasetA, "aaaaaaaa-0000-0000-0000-000000000002")

	chain := buildFixtureChain(t, full, incremental)
	resolution := chain.Resolve(time.Unix(20, 0).UTC())
	require.Equal(t, StatusTopologyBoundary, resolution.Status)
	require.Nil(t, resolution.Point)
	require.Equal(t, "before", resolution.Before.Name)
	require.Equal(t, "after", resolution.After.Name)
}

func TestResolveChainBrokenGapBetweenGroups(t *testing.T) {
	// Two unrelated fulls form two segments split by a chain break; T between them
	// reports chain_broken with Point = last point before the break.
	fullA := manifestFixture("full-a", "", "full-a", backup.BackupTypeFull, 10, 0, 10,
		clusterPointFixture("early", 100, 10))
	fullB := manifestFixture("full-b", "", "full-b", backup.BackupTypeFull, 40, 0, 10,
		clusterPointFixture("late", 200, 40))

	chain := buildFixtureChain(t, fullA, fullB)
	resolution := chain.Resolve(time.Unix(25, 0).UTC())
	require.Equal(t, StatusChainBroken, resolution.Status)
	require.Equal(t, "early", resolution.Point.Name)
	require.Equal(t, "early", resolution.Before.Name)
	require.Equal(t, "late", resolution.After.Name)
}

func TestResolveOKWithinSegmentIgnoresLaterGap(t *testing.T) {
	// A point ≤ T in the same segment resolves OK despite a later gapped segment.
	fullA := manifestFixture("full-a", "", "full-a", backup.BackupTypeFull, 10, 0, 10,
		clusterPointFixture("early", 100, 10))
	fullB := manifestFixture("full-b", "", "full-b", backup.BackupTypeFull, 40, 0, 10,
		clusterPointFixture("late", 200, 40))

	chain := buildFixtureChain(t, fullA, fullB)
	resolution := chain.Resolve(time.Unix(10, 0).UTC())
	require.Equal(t, StatusOK, resolution.Status)
	require.Equal(t, "early", resolution.Point.Name)
}
