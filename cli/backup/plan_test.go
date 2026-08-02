package backup

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testRSa      = "aaaaaaaa-0000-0000-0000-000000000001"
	testRSb      = "bbbbbbbb-0000-0000-0000-000000000002"
	testInstA    = "aaaaaaaa-1111-1111-1111-111111111111"
	testInstB    = "bbbbbbbb-1111-1111-1111-111111111111"
	shardID      = "bk-1"
	instanceName = "a-001"
)

func rwInst(uuid, name string) LiveInstance {
	return LiveInstance{InstanceUUID: uuid, InstanceName: name, Mode: "rw", Status: "OK"}
}

func roInst(uuid, name string) LiveInstance {
	return LiveInstance{InstanceUUID: uuid, InstanceName: name, Mode: "ro", Status: "OK"}
}

// downInst is how cluster discovery reports an instance it could not reach.
func downInst(uuid, name string) LiveInstance {
	return LiveInstance{
		InstanceUUID: uuid,
		InstanceName: name,
		Mode:         "unknown",
		Status:       "not reachable",
	}
}

func liveTopo(rs map[string][]LiveInstance) *LiveTopology {
	return &LiveTopology{Replicasets: rs}
}

func manifestWithShard() *ClusterManifest {
	return &ClusterManifest{
		SchemaVersion: SchemaVersion,
		BackupID:      BackupID(shardID),
		Status:        StatusOK,
		Shards: map[string]Shard{
			testRSa: {Instance: &ShardInstance{
				InstanceUUID: testInstA,
				InstanceName: instanceName,
				VclockEnd:    Vclock{1: 100},
				Artifact:     Artifact{Type: BackupTypeFull, RecoveryPoints: []RecoveryPoint{}},
			}},
		},
		Topology: Topology{Replicasets: map[string][]TopologyInstance{
			testRSa: {{InstanceUUID: testInstA, InstanceName: instanceName}},
		}},
		Warnings: []Warning{},
	}
}

func TestPlanFullSelectsFirstRW(t *testing.T) {
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {
			roInst(testInstB, "a-002"),
			rwInst(testInstA, "a-001"),
		},
	})

	plan, err := Plan(BackupTypeFull, nil, live)
	require.NoError(t, err)
	assert.Equal(t, BackupTypeFull, plan.Type)
	assert.Empty(t, plan.PreviousBackupID)
	assert.Nil(t, plan.Replicasets[testRSa].FromVclock)
	assert.Equal(t, testInstA, plan.Replicasets[testRSa].MasterInstanceUUID)
}

func TestPlanFullMasterMasterPicksFirst(t *testing.T) {
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {
			rwInst(testInstA, "a-001"),
			rwInst(testInstB, "a-002"),
		},
	})

	plan, err := Plan(BackupTypeFull, nil, live)
	require.NoError(t, err)
	assert.Equal(t, testInstA, plan.Replicasets[testRSa].MasterInstanceUUID)
}

func TestPlanFullNoRW(t *testing.T) {
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {roInst(testInstA, "a-001")},
	})

	_, err := Plan(BackupTypeFull, nil, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoMaster)
}

func TestPlanIncrementalValid(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {rwInst(testInstA, "a-001")},
	})

	plan, err := Plan(BackupTypeIncremental, latest, live)
	require.NoError(t, err)
	assert.Equal(t, BackupTypeIncremental, plan.Type)
	assert.Equal(t, BackupID("bk-1"), plan.PreviousBackupID)
	assert.Equal(t, Vclock{1: 100}, plan.Replicasets[testRSa].FromVclock)
}

func TestPlanIncrementalNilLatest(t *testing.T) {
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {rwInst(testInstA, "a-001")},
	})

	_, err := Plan(BackupTypeIncremental, nil, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoBackups)
}

func TestPlanIncrementalDegradedShard(t *testing.T) {
	latest := &ClusterManifest{
		SchemaVersion: SchemaVersion,
		BackupID:      "bk-1",
		Status:        StatusOK,
		Shards: map[string]Shard{
			testRSa: {Error: "unreachable"},
		},
		Topology: Topology{Replicasets: map[string][]TopologyInstance{
			testRSa: {{InstanceUUID: "unknown"}},
		}},
		Warnings: []Warning{},
	}
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {rwInst(testInstA, "a-001")},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrShardDegraded)
}

func TestPlanIncrementalReplicasetAdded(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {rwInst(testInstA, "a-001")},
		testRSb: {rwInst(testInstB, "b-001")},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTopologyChanged)
	assert.Contains(t, err.Error(), "added")
}

func TestPlanIncrementalReplicasetRemoved(t *testing.T) {
	latest := manifestWithShard()
	latest.Shards[testRSb] = Shard{Instance: &ShardInstance{
		InstanceUUID: testInstB,
		InstanceName: "b-001",
		VclockEnd:    Vclock{2: 200},
		Artifact:     Artifact{Type: BackupTypeFull, RecoveryPoints: []RecoveryPoint{}},
	}}
	latest.Topology.Replicasets[testRSb] = []TopologyInstance{{
		InstanceUUID: testInstB, InstanceName: "b-001",
	}}

	live := liveTopo(map[string][]LiveInstance{
		testRSa: {rwInst(testInstA, "a-001")},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTopologyChanged)
	assert.Contains(t, err.Error(), "removed")
}

func TestPlanIncrementalMasterChangedToRO(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {
			roInst(testInstA, "a-001"),
			rwInst(testInstB, "a-002"),
		},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMasterChanged)
	assert.Contains(t, err.Error(), "no longer RW")
}

// TestPlanIncrementalMasterUnreachable checks a shard that could not be
// reached is not diagnosed as a master change: an orchestrator obeying
// "use --target=full" would answer a restart with a new full chain of the
// whole cluster.
func TestPlanIncrementalMasterUnreachable(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {
			downInst(testInstA, "a-001"),
			downInst(testInstB, "a-002"),
		},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReplicasetUnreachable)
	assert.NotErrorIs(t, err, ErrMasterChanged)
	assert.NotContains(t, err.Error(), "--target=full")
}

// TestPlanIncrementalReplicasetNoLiveView checks a replicaset discovery
// returned no reachable instance for is reported as unreachable, not as a
// master change.
func TestPlanIncrementalReplicasetNoLiveView(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {downInst(testInstB, "a-002")},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReplicasetUnreachable)
	assert.NotContains(t, err.Error(), "--target=full")
}

// TestPlanIncrementalMasterDownAfterFailover checks that a down master with
// another instance already RW is a real master change: retrying would never
// produce an increment.
func TestPlanIncrementalMasterDownAfterFailover(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {
			downInst(testInstA, "a-001"),
			rwInst(testInstB, "a-002"),
		},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMasterChanged)
	assert.Contains(t, err.Error(), "--target=full")
}

func TestPlanIncrementalMasterInstanceGone(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {rwInst(testInstB, "a-002")},
	})

	_, err := Plan(BackupTypeIncremental, latest, live)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMasterChanged)
	assert.Contains(t, err.Error(), "not found in live topology")
}

func TestPlanIncrementalMasterStillRW(t *testing.T) {
	latest := manifestWithShard()
	live := liveTopo(map[string][]LiveInstance{
		testRSa: {
			rwInst(testInstA, "a-001"),
			rwInst(testInstB, "a-002"),
		},
	})

	plan, err := Plan(BackupTypeIncremental, latest, live)
	require.NoError(t, err)
	assert.Equal(t, testInstA, plan.Replicasets[testRSa].MasterInstanceUUID)
}

func TestPlanFullJSONNoFromVclockNoPrevious(t *testing.T) {
	plan := &BackupPlan{
		Type: BackupTypeFull,
		Replicasets: map[string]ReplicasetPlan{
			testRSa: {MasterInstanceUUID: testInstA, MasterInstanceName: "a-001"},
		},
	}

	data, err := json.MarshalIndent(plan, "", "  ")
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "full", parsed["mode"])
	_, hasPrev := parsed["previous_backup_id"]
	assert.False(t, hasPrev)
	rs := parsed["replicasets"].(map[string]any)[testRSa].(map[string]any)
	_, hasVclock := rs["from_vclock"]
	assert.False(t, hasVclock)
}

func TestPlanIncrementalJSONHasFromVclockAndPrevious(t *testing.T) {
	plan := &BackupPlan{
		Type:             BackupTypeIncremental,
		PreviousBackupID: "20260312T110000Z",
		Replicasets: map[string]ReplicasetPlan{
			testRSa: {
				MasterInstanceUUID: testInstA,
				MasterInstanceName: "router-001",
				FromVclock:         Vclock{1: 1502, 2: 230},
			},
		},
	}

	data, err := json.MarshalIndent(plan, "", "  ")
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))
	assert.Equal(t, "incremental", parsed["mode"])
	assert.Equal(t, "20260312T110000Z", parsed["previous_backup_id"])
	rs := parsed["replicasets"].(map[string]any)[testRSa].(map[string]any)
	assert.NotNil(t, rs["from_vclock"])
}
