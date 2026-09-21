package chain

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

const (
	replicasetR = "33333333-3333-3333-3333-333333333333"
	masterR     = "cccccccc-0000-0000-0000-000000000001"

	instanceA = "storage-a-001"
	instanceB = "storage-b-001"
	instanceR = "router-001"
)

// replicasetFixture describes one replicaset of a fixture manifest: the UUID it
// is backed up under, its master, the instance name a selection addresses it
// by, and the recovery points its shard carries.
type replicasetFixture struct {
	uuid     string
	master   string
	instance string
	points   []backup.RecoveryPoint
}

// namedFixture builds a manifest whose topology instances carry names. The
// shared fixtures leave them nameless, and a selection can only address an
// instance that has a name.
func namedFixture(
	id, previous, base string,
	backupType backup.BackupType,
	createdAt, vclockBegin, vclockEnd int64,
	replicasets ...replicasetFixture,
) backup.ClusterManifest {
	manifest := backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         backup.BackupID(id),
		PreviousBackupID: backup.OptionalBackupID(previous),
		BaseFullBackupID: backup.BackupID(base),
		Status:           backup.StatusOK,
		CreationTime:     time.Unix(createdAt, 0).UTC(),
		Shards:           make(map[string]backup.Shard, len(replicasets)),
		Topology: backup.Topology{
			Replicasets: make(map[string][]backup.TopologyInstance, len(replicasets)),
		},
		Warnings: []backup.Warning{},
	}

	for i, replicaset := range replicasets {
		manifest.Topology.Replicasets[replicaset.uuid] = []backup.TopologyInstance{{
			InstanceUUID: replicaset.master,
			InstanceName: replicaset.instance,
		}}

		addShard(&manifest, replicaset.uuid, replicaset.master, uint32(i+1),
			backupType, vclockBegin, vclockEnd, replicaset.points)
	}

	return manifest
}

// shardedFixture builds a manifest for two storage replicasets and a router
// replicaset, with the label on the storages only - the shape a stateless
// router gives a backup.
func shardedFixture(label string) backup.ClusterManifest {
	return namedFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		replicasetFixture{
			uuid:     replicasetA,
			master:   masterA,
			instance: instanceA,
			points:   []backup.RecoveryPoint{recoveryPoint(label, 1, 100, 10)},
		},
		replicasetFixture{
			uuid:     replicasetB,
			master:   masterB,
			instance: instanceB,
			points:   []backup.RecoveryPoint{recoveryPoint(label, 2, 100, 11)},
		},
		replicasetFixture{uuid: replicasetR, master: masterR, instance: instanceR},
	)
}

// buildSelectedChain builds a chain over the given selection.
func buildSelectedChain(
	t *testing.T,
	selection *Selection,
	manifests ...backup.ClusterManifest,
) *Chain {
	t.Helper()

	pointers := make([]*backup.ClusterManifest, len(manifests))
	for i := range manifests {
		pointers[i] = &manifests[i]
	}

	chain, err := Build(pointers, WithSelection(selection))
	require.NoError(t, err)

	return chain
}

func TestClusterPointsWithoutSelectionRequireEveryReplicaset(t *testing.T) {
	// The router carries no recovery points, so nothing is cluster-wide.
	full := shardedFixture("point")

	require.Empty(t, buildFixtureChain(t, full).ClusterPoints())
}

func TestClusterPointsSelectionExcludesUnselectedReplicaset(t *testing.T) {
	full := shardedFixture("point")

	points := buildSelectedChain(t,
		SelectInstances([]string{instanceA, instanceB}), full).ClusterPoints()

	require.Len(t, points, 1)
	require.Equal(t, "point", points[0].Name)
	require.Equal(t, time.Unix(10, 0).UTC(), points[0].Timestamp)
	assert.Equal(t, Position{ReplicaID: 1, LSN: 100}, points[0].Shards[replicasetA])
	assert.Equal(t, Position{ReplicaID: 2, LSN: 100}, points[0].Shards[replicasetB])
	assert.ElementsMatch(t,
		[]string{replicasetA, replicasetB}, slices.Collect(maps.Keys(points[0].Shards)))

	// The point still carries the whole segment topology; a consumer that wants
	// the restored replicasets alone narrows it by the keys of Shards.
	assert.Equal(t, full.Topology, points[0].Topology)
	assert.Len(t, points[0].Topology.Replicasets, 3)
}

func TestClusterPointsSelectionRequiresEverySelectedReplicaset(t *testing.T) {
	// The label is on one selected storage only: still not cluster-wide.
	full := namedFixture("full", "", "full", backup.BackupTypeFull, 20, 0, 20,
		replicasetFixture{
			uuid:     replicasetA,
			master:   masterA,
			instance: instanceA,
			points:   []backup.RecoveryPoint{recoveryPoint("point", 1, 100, 10)},
		},
		replicasetFixture{uuid: replicasetB, master: masterB, instance: instanceB},
		replicasetFixture{uuid: replicasetR, master: masterR, instance: instanceR},
	)

	points := buildSelectedChain(t,
		SelectInstances([]string{instanceA, instanceB}), full).ClusterPoints()

	require.Empty(t, points)
}

func TestClusterPointsSelectionOutsideSegmentHasNoPoints(t *testing.T) {
	// No replicaset of the segment answers to the selected names, so there is no
	// set of replicasets that could agree on a point.
	full := shardedFixture("point")

	points := buildSelectedChain(t,
		SelectInstances([]string{"storage-c-001"}), full).ClusterPoints()

	require.Empty(t, points)
}

func TestClusterPointsSelectionResolvesPerSegment(t *testing.T) {
	// A redeployed cluster keeps its instance names and gets new replicaset
	// UUIDs, so each segment resolves the selection against its own topology.
	const (
		redeployedA = "aaaa1111-1111-1111-1111-111111111111"
		redeployedB = "bbbb2222-2222-2222-2222-222222222222"
	)

	before := shardedFixture("before")
	after := namedFixture("full-after", "", "full-after", backup.BackupTypeFull, 40, 0, 20,
		replicasetFixture{
			uuid:     redeployedA,
			master:   masterA,
			instance: instanceA,
			points:   []backup.RecoveryPoint{recoveryPoint("after", 1, 200, 30)},
		},
		replicasetFixture{
			uuid:     redeployedB,
			master:   masterB,
			instance: instanceB,
			points:   []backup.RecoveryPoint{recoveryPoint("after", 2, 200, 31)},
		},
		replicasetFixture{uuid: replicasetR, master: masterR, instance: instanceR},
	)

	chain := buildSelectedChain(t,
		SelectInstances([]string{instanceA, instanceB}), before, after)

	points := chain.ClusterPoints()
	require.Len(t, points, 2)

	require.Equal(t, "before", points[0].Name)
	assert.ElementsMatch(t,
		[]string{replicasetA, replicasetB}, slices.Collect(maps.Keys(points[0].Shards)))

	require.Equal(t, "after", points[1].Name)
	assert.ElementsMatch(t,
		[]string{redeployedA, redeployedB}, slices.Collect(maps.Keys(points[1].Shards)))
}
