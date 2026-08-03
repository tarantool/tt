package restore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPlanJSONShapeOK checks the printed document against the RFC's status=ok
// example. The orchestrator reads this by key, so the shape is as much of a
// contract as the exit code is.
func TestPlanJSONShapeOK(t *testing.T) {
	f := newPlanFixture(t)
	f.chainWithAnUnreachableShard()

	document := asJSON(t, f.plan(550, masterOnlyCluster()))

	require.Equal(t,
		[]string{
			"download_plan", "recovery_point", "restore_targets", "status",
			"target_time", "warnings",
		},
		keysOf(t, document))
	require.Equal(t, "ok", document["status"])
	require.Equal(t, "1970-01-01T00:09:10Z", document["target_time"])
	require.Empty(t, document["warnings"], "warnings is always present, empty on success")

	// The point's identifier is spelled label, not the example's name: what
	// Tarantool records is a label, and `tt restore apply --point-name` takes it.
	point := document["recovery_point"].(map[string]any)
	require.Equal(t, []string{"label", "timestamp", "trim_to_by_replicaset"}, keysOf(t, point))
	require.NotContains(t, point, "name")
	require.Equal(t, "p3", point["label"])
	require.Equal(t, "1970-01-01T00:08:30Z", point["timestamp"])

	positions := point["trim_to_by_replicaset"].(map[string]any)
	require.Equal(t, []string{shardA, shardB}, keysOf(t, positions))
	require.Equal(t, map[string]any{"replica_id": float64(replicaOfA), "lsn": float64(2500)},
		positions[shardA])

	plan := document["download_plan"].(map[string]any)
	require.Equal(t, []string{shardA, shardB}, keysOf(t, plan))

	chainOfA := plan[shardA].([]any)
	require.Len(t, chainOfA, 3)

	// Everything but the last archive of a chain is replayed whole, so the trim
	// keys are absent rather than present and false.
	for _, item := range chainOfA[:2] {
		require.Equal(t,
			[]string{"artifact", "backup_id", "checksum_sha256", "type"},
			keysOf(t, item))
	}

	last := chainOfA[2].(map[string]any)
	require.Equal(t,
		[]string{"artifact", "backup_id", "checksum_sha256", "trim_to", "trim_xlog", "type"},
		keysOf(t, last))
	require.Equal(t, incTwoID, last["backup_id"])
	require.Equal(t, "incremental", last["type"])
	require.Equal(t, true, last["trim_xlog"])
	require.Equal(t,
		map[string]any{"replica_id": float64(replicaOfA), "lsn": float64(2500)},
		last["trim_to"])

	// The sum travels with every archive: tt restore apply --checksums
	// re-checks it on the node, after a copy this command knows nothing about.
	for _, shard := range []string{shardA, shardB} {
		for _, entry := range plan[shard].([]any) {
			item := entry.(map[string]any)
			require.NotEmpty(t, item["checksum_sha256"])
			require.NotEmpty(t, item["artifact"])
			require.NotEmpty(t, item["backup_id"])
		}
	}

	// The replicaset that was unreachable for the middle backup replays two
	// archives where the other replays three, exactly as the RFC example shows.
	require.Len(t, plan[shardB].([]any), 2)

	// restore_targets is keyed like download_plan, so one replicaset uuid
	// answers both what a node replays and what it must come up as.
	targets := document["restore_targets"].(map[string]any)
	require.Equal(t, []string{shardA, shardB}, keysOf(t, targets))

	targetOfA := targets[shardA].(map[string]any)
	require.Equal(t, []string{"instance_name", "patch_uuid"}, keysOf(t, targetOfA))
	require.Equal(t, masterOfA, targetOfA["instance_name"])
	require.Equal(t, instanceUUIDOf("deploy-1", masterOfA), targetOfA["patch_uuid"])
}

// TestPlanJSONShapeRestoreTargetsWithRejoin checks the section a realistic
// configuration produces: one node per replicaset replays the archives, the
// rest are named as the nodes to wipe. The key is absent whenever the list
// would be empty, which a run without -c cannot be told apart from a
// replicaset with nothing to wipe - the warning about the skipped topology
// check is what says which of the two it was.
func TestPlanJSONShapeRestoreTargetsWithRejoin(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	withConfig := asJSON(t, f.plan(550, liveCluster()))
	targets := withConfig["restore_targets"].(map[string]any)

	targetOfA := targets[shardA].(map[string]any)
	require.Equal(t, []string{"instance_name", "patch_uuid", "rejoin"},
		keysOf(t, targetOfA))
	require.Equal(t, []any{secondOfA}, targetOfA["rejoin"])

	withoutConfig := asJSON(t, f.plan(550, nil))
	bare := withoutConfig["restore_targets"].(map[string]any)[shardA].(map[string]any)
	require.Equal(t, []string{"instance_name", "patch_uuid"}, keysOf(t, bare))
}

// TestPlanJSONShapeTopologyBoundary checks the printed document against the
// RFC's status=topology_boundary example.
func TestPlanJSONShapeTopologyBoundary(t *testing.T) {
	f := newPlanFixture(t)
	f.chainAcrossAMasterChange()

	document := asJSON(t, f.plan(400, masterOnlyCluster()))

	// The example carries status, reason and nearest_safe; target_time and
	// warnings are on every document this command prints, failures included.
	require.Equal(t,
		[]string{"nearest_safe", "reason", "status", "target_time", "warnings"},
		keysOf(t, document))
	require.Equal(t, "topology_boundary", document["status"])
	require.Equal(t, "target time falls between manifests with different topology",
		document["reason"])

	nearest := document["nearest_safe"].(map[string]any)
	require.Equal(t, []string{"after", "before"}, keysOf(t, nearest))
	require.Equal(t, "1970-01-01T00:01:50Z", nearest["before"])
	require.Equal(t, "1970-01-01T00:08:30Z", nearest["after"])
}

// TestPlanJSONShapeTopologyMismatch checks the sixth status: the one the RFC's
// table does not list, whose diff is what an operator acts on.
func TestPlanJSONShapeTopologyMismatch(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	document := asJSON(t, f.plan(550, configuredCluster(
		configuredReplicaset("storage-a", masterOfA),
	)))

	require.Equal(t,
		[]string{"reason", "status", "target_time", "topology_diff", "warnings"},
		keysOf(t, document))
	require.Equal(t, "topology_mismatch", document["status"])

	diff := document["topology_diff"].(map[string]any)
	require.Equal(t, []string{"missing_replicasets"}, keysOf(t, diff))

	missing := diff["missing_replicasets"].([]any)
	require.Len(t, missing, 1)
	require.Equal(t, map[string]any{
		"replicaset_uuid": shardB,
		"instances":       []any{masterOfB},
	}, missing[0])
}

// TestPlanJSONOmitsNearestSafeWhenThereIsNone keeps a failed plan from carrying
// an empty hint: an orchestrator that shows nearest_safe to a human must be
// able to tell "here is another point" from "there is none".
func TestPlanJSONOmitsNearestSafeWhenThereIsNone(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	document := asJSON(t, f.plan(550, configuredCluster(
		configuredReplicaset("storage-a", masterOfA),
		configuredReplicaset("storage-b", masterOfB),
		configuredReplicaset("storage-c", masterOfC),
	)))

	require.NotContains(t, document, "nearest_safe")
	require.NotContains(t, document, "recovery_point")
	require.NotContains(t, document, "download_plan")
}
