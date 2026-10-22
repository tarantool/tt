package restore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPlanJSONShapeOK checks the printed document against the RFC's status=ok
// example. The orchestrator reads this by key, so the shape is as much of a
// contract as the exit code is.
func TestPlanJSONShapeOK(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.chainWithAnUnreachableShard()

	document := asJSON(t, fixture.plan(550, masterOnlyCluster()))

	require.Equal(t,
		[]string{
			"download_plan", "recovery_point", "restore_targets", "status",
			"target_time", "warnings",
		},
		keysOf(t, document))
	require.Equal(t, "ok", document["status"])
	require.Equal(t, "1970-01-01T00:09:10Z", document["target_time"])
	// Both manifests of the chain record no members, so the plan says so:
	// warnings is always present, and here carries the single-target notes.
	require.Equal(t, []any{
		noMembersWarning(incTwoID, shardA),
		noMembersWarning(incTwoID, shardB),
	}, document["warnings"])

	// The point's identifier is spelled label, not the example's name: what
	// Tarantool records is a label, and `tt restore apply --point-name` takes it.
	point := requireType[map[string]any](t, document["recovery_point"])
	require.Equal(t, []string{"label", "timestamp", "trim_to_by_replicaset"}, keysOf(t, point))
	require.NotContains(t, point, "name")
	require.Equal(t, "p3", point["label"])
	require.Equal(t, "1970-01-01T00:08:30Z", point["timestamp"])

	positions := requireType[map[string]any](t, point["trim_to_by_replicaset"])
	require.Equal(t, []string{shardA, shardB}, keysOf(t, positions))
	require.Equal(t, map[string]any{"replica_id": float64(replicaOfA), "lsn": float64(2500)},
		positions[shardA])

	plan := requireType[map[string]any](t, document["download_plan"])
	require.Equal(t, []string{shardA, shardB}, keysOf(t, plan))

	chainOfA := requireType[[]any](t, plan[shardA])
	require.Len(t, chainOfA, 3)

	// Everything but the last archive of a chain is replayed whole, so the trim
	// keys are absent rather than present and false.
	for _, item := range chainOfA[:2] {
		require.Equal(t,
			[]string{"artifact", "backup_id", "checksum_sha256", "type"},
			keysOf(t, item))
	}

	last := requireType[map[string]any](t, chainOfA[2])
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
		for _, entry := range requireType[[]any](t, plan[shard]) {
			item := requireType[map[string]any](t, entry)
			require.NotEmpty(t, item["checksum_sha256"])
			require.NotEmpty(t, item["artifact"])
			require.NotEmpty(t, item["backup_id"])
		}
	}

	// The replicaset that was unreachable for the middle backup replays two
	// archives where the other replays three, exactly as the RFC example shows.
	require.Len(t, requireType[[]any](t, plan[shardB]), 2)

	// restore_targets is keyed like download_plan, so one replicaset uuid
	// answers both what a node replays and what it must come up as.
	targets := requireType[map[string]any](t, document["restore_targets"])
	require.Equal(t, []string{shardA, shardB}, keysOf(t, targets))

	targetOfA := requireType[map[string]any](t, targets[shardA])
	require.Equal(t, []string{"instance_name", "instances", "patch_uuid"},
		keysOf(t, targetOfA))
	require.Equal(t, masterOfA, targetOfA["instance_name"])
	require.Equal(t, instanceUUIDOf("deploy-1", masterOfA), targetOfA["patch_uuid"])

	// With no members recorded the master is the whole instance list, his own
	// entry repeated under instances with his role spelled out.
	instances := requireType[[]any](t, targetOfA["instances"])
	require.Len(t, instances, 1)
	require.Equal(t, map[string]any{
		"instance_name": masterOfA,
		"patch_uuid":    instanceUUIDOf("deploy-1", masterOfA),
		"role":          "master",
	}, instances[0])
}

// TestPlanJSONShapeRestoreTargetsWithRejoin checks the section a realistic
// configuration produces: one node per replicaset replays the archives, the
// rest are named as the nodes to wipe. The key is absent whenever the list
// would be empty, which a run without -c cannot be told apart from a
// replicaset with nothing to wipe - the warning about the skipped topology
// check is what says which of the two it was.
func TestPlanJSONShapeRestoreTargetsWithRejoin(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	withConfig := asJSON(t, fixture.plan(550, liveCluster()))
	targets := requireType[map[string]any](t, withConfig["restore_targets"])

	targetOfA := requireType[map[string]any](t, targets[shardA])
	require.Equal(t,
		[]string{"instance_name", "instances", "patch_uuid", "rejoin"},
		keysOf(t, targetOfA))
	require.Equal(t, []any{secondOfA}, targetOfA["rejoin"])

	withoutConfig := asJSON(t, fixture.plan(550, nil))
	bareTargets := requireType[map[string]any](t, withoutConfig["restore_targets"])
	bare := requireType[map[string]any](t, bareTargets[shardA])
	require.Equal(t, []string{"instance_name", "instances", "patch_uuid"},
		keysOf(t, bare))
}

// TestPlanJSONShapeTopologyBoundary checks the printed document against the
// RFC's status=topology_boundary example.
func TestPlanJSONShapeTopologyBoundary(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.chainAcrossAMasterChange()

	document := asJSON(t, fixture.plan(400, masterOnlyCluster()))

	// The example carries status, reason and nearest_safe; target_time and
	// warnings are on every document this command prints, failures included.
	require.Equal(t,
		[]string{"nearest_safe", "reason", "status", "target_time", "warnings"},
		keysOf(t, document))
	require.Equal(t, "topology_boundary", document["status"])
	require.Equal(t, "target time falls between manifests with different topology",
		document["reason"])

	nearest := requireType[map[string]any](t, document["nearest_safe"])
	require.Equal(t, []string{"after", "before"}, keysOf(t, nearest))
	require.Equal(t, "1970-01-01T00:01:50Z", nearest["before"])
	require.Equal(t, "1970-01-01T00:08:30Z", nearest["after"])
}

// TestPlanJSONShapeTopologyMismatch checks the sixth status: the one the RFC's
// table does not list, whose diff is what an operator acts on.
func TestPlanJSONShapeTopologyMismatch(t *testing.T) {
	fixture := newPlanFixture(t)
	fixture.standardChain()

	document := asJSON(t, fixture.plan(550, configuredCluster(
		configuredReplicaset("storage-a", masterOfA),
	)))

	require.Equal(t,
		[]string{"reason", "status", "target_time", "topology_diff", "warnings"},
		keysOf(t, document))
	require.Equal(t, "topology_mismatch", document["status"])

	diff := requireType[map[string]any](t, document["topology_diff"])
	require.Equal(t, []string{"missing_replicasets"}, keysOf(t, diff))

	missing := requireType[[]any](t, diff["missing_replicasets"])
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
	fixture := newPlanFixture(t)
	fixture.standardChain()

	document := asJSON(t, fixture.plan(550, configuredCluster(
		configuredReplicaset("storage-a", masterOfA),
		configuredReplicaset("storage-b", masterOfB),
		configuredReplicaset("storage-c", masterOfC),
	)))

	require.NotContains(t, document, "nearest_safe")
	require.NotContains(t, document, "recovery_point")
	require.NotContains(t, document, "download_plan")
}

// requireType keeps JSON shape assertions explicit before indexing a value.
func requireType[T any](t *testing.T, value any) T {
	t.Helper()

	typed, ok := value.(T)
	require.True(t, ok, "unexpected JSON value type: %T", value)

	return typed
}
