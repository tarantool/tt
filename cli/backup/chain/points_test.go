package chain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
)

func TestClusterPointsJoinReplicasetsByName(t *testing.T) {
	// Same point name on both shards -> one ClusterPoint.
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		map[string][]backup.RecoveryPoint{
			replicasetA: {recoveryPoint("point", 1, 100, 11)},
			replicasetB: {recoveryPoint("point", 2, 200, 12)},
		})

	points := buildFixtureChain(t, manifest).ClusterPoints()
	require.Len(t, points, 1)
	require.Equal(t, "point", points[0].Name)
	require.Equal(t, time.Unix(11, 0).UTC(), points[0].Timestamp)
	require.Equal(t, Position{ReplicaID: 1, LSN: 100}, points[0].Shards[replicasetA])
	require.Equal(t, Position{ReplicaID: 2, LSN: 200}, points[0].Shards[replicasetB])
}

func TestClusterPointsDropIncompletePoint(t *testing.T) {
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		map[string][]backup.RecoveryPoint{
			replicasetA: {recoveryPoint("partial", 1, 100, 11)},
			// replicasetB has no recovery points at all.
		})

	require.Empty(t, buildFixtureChain(t, manifest).ClusterPoints())
}

func TestClusterPointsMultiplePointsInOneManifest(t *testing.T) {
	// Two names present on all shards → two ClusterPoints.
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		map[string][]backup.RecoveryPoint{
			replicasetA: {
				recoveryPoint("first", 1, 100, 10),
				recoveryPoint("second", 1, 200, 20),
			},
			replicasetB: {
				recoveryPoint("first", 2, 100, 11),
				recoveryPoint("second", 2, 200, 21),
			},
		})

	points := buildFixtureChain(t, manifest).ClusterPoints()
	require.Len(t, points, 2)
	require.Equal(t, "first", points[0].Name)
	require.Equal(t, "second", points[1].Name)
}

func TestClusterPointsOrderedByTimestamp(t *testing.T) {
	manifest := manifestFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		map[string][]backup.RecoveryPoint{
			replicasetA: {
				recoveryPoint("later", 1, 200, 20),
				recoveryPoint("earlier", 1, 100, 10),
			},
			replicasetB: {
				recoveryPoint("later", 2, 200, 21),
				recoveryPoint("earlier", 2, 100, 11),
			},
		})

	points := buildFixtureChain(t, manifest).ClusterPoints()
	require.Len(t, points, 2)
	require.Equal(t, "earlier", points[0].Name)
	require.Equal(t, "later", points[1].Name)
	require.True(t, points[0].Timestamp.Before(points[1].Timestamp))
}

func TestClusterPointsDoNotCrossTopologyBoundary(t *testing.T) {
	// "point" is on all shards of each manifest, but a topology boundary between
	// them prevents cross-manifest stitching: two points, not one.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10,
		clusterPointFixture("point", 100, 8))
	incremental := manifestFixture("inc", "full", "full", backup.BackupTypeIncremental, 20, 10, 20,
		clusterPointFixture("point", 200, 18))
	changeMaster(&incremental, replicasetA, "aaaaaaaa-0000-0000-0000-000000000002")

	points := buildFixtureChain(t, full, incremental).ClusterPoints()
	require.Len(t, points, 2)
	require.Equal(t, time.Unix(8, 0).UTC(), points[0].Timestamp)
	require.Equal(t, time.Unix(18, 0).UTC(), points[1].Timestamp)
}

func TestClusterPointsExcludeProblematicEntries(t *testing.T) {
	// A problematic entry is excluded from stitching even with valid points.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10, nil)
	broken := manifestFixture("broken", "full", "full", backup.BackupTypeIncremental, 20, 9, 20,
		clusterPointFixture("point", 18, 18))

	require.Empty(t, buildFixtureChain(t, full, broken).ClusterPoints())
}

func changeMaster(manifest *backup.ClusterManifest, replicasetUUID, instanceUUID string) {
	shard := manifest.Shards[replicasetUUID]
	shard.Instance.InstanceUUID = instanceUUID
	manifest.Shards[replicasetUUID] = shard
	manifest.Topology.Replicasets[replicasetUUID] = []backup.TopologyInstance{{
		InstanceUUID: instanceUUID,
	}}
}

func addReplica(manifest *backup.ClusterManifest, replicasetUUID, replicaInstanceUUID string) {
	instances := manifest.Topology.Replicasets[replicasetUUID]
	instances = append(instances, backup.TopologyInstance{InstanceUUID: replicaInstanceUUID})
	manifest.Topology.Replicasets[replicasetUUID] = instances
}

func TestClusterPointsDropPointWhenShardIsHole(t *testing.T) {
	// replicasetB is a hole across the segment: "point" on replicasetA alone is
	// not cluster-wide, since the topology still requires both.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		map[string][]backup.RecoveryPoint{
			replicasetA: {recoveryPoint("point", 1, 100, 11)},
		})
	makeShardHole(&full)

	require.Empty(t, buildFixtureChain(t, full).ClusterPoints())
}

func TestClusterPointsStitchAcrossManifestsInSegment(t *testing.T) {
	// One name may come from different replicasets in different manifests of a
	// segment and still stitch into one point.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10,
		map[string][]backup.RecoveryPoint{
			replicasetA: {recoveryPoint("point", 1, 100, 10)},
		})
	incremental := manifestFixture("inc", "full", "full", backup.BackupTypeIncremental, 20, 10, 20,
		map[string][]backup.RecoveryPoint{
			replicasetB: {recoveryPoint("point", 2, 200, 12)},
		})

	points := buildFixtureChain(t, full, incremental).ClusterPoints()
	require.Len(t, points, 1)
	require.Equal(t, "point", points[0].Name)
	require.Equal(t, time.Unix(10, 0).UTC(), points[0].Timestamp)
	require.Equal(t, Position{ReplicaID: 1, LSN: 100}, points[0].Shards[replicasetA])
	require.Equal(t, Position{ReplicaID: 2, LSN: 200}, points[0].Shards[replicasetB])
}

func TestClusterPointsCarrySegmentTopology(t *testing.T) {
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		clusterPointFixture("point", 100, 8))

	points := buildFixtureChain(t, full).ClusterPoints()
	require.Len(t, points, 1)
	require.Equal(t, full.Topology, points[0].Topology)
}

func TestClusterPointsAcrossTopologyBoundaryCarryDifferentTopologies(t *testing.T) {
	// Points on either side of a boundary carry their own segment's topology.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10,
		clusterPointFixture("before", 100, 8))
	incremental := manifestFixture("inc", "full", "full", backup.BackupTypeIncremental, 20, 10, 20,
		clusterPointFixture("after", 200, 18))
	changeMaster(&incremental, replicasetA, "aaaaaaaa-0000-0000-0000-000000000002")

	chain := buildFixtureChain(t, full, incremental)
	before := findPoint(t, chain, "before")
	after := findPoint(t, chain, "after")

	require.Equal(t, full.Topology, before.Topology)
	require.Equal(t, incremental.Topology, after.Topology)
	require.NotEqual(t, before.Topology, after.Topology)
}

func TestClusterPointsIgnoreReplicaSetChange(t *testing.T) {
	// Adding a non-master replica is not a topology boundary.
	full := manifestFixture("full", "", "full", backup.BackupTypeFull, 10, 0, 10,
		clusterPointFixture("point", 100, 8))
	incremental := manifestFixture("inc", "full", "full", backup.BackupTypeIncremental, 20, 10, 20,
		clusterPointFixture("point", 200, 18))
	addReplica(&incremental, replicasetA, "aaaaaaaa-0000-0000-0000-000000000099")

	points := buildFixtureChain(t, full, incremental).ClusterPoints()
	require.Len(t, points, 1)
	require.Equal(t, "point", points[0].Name)
}
