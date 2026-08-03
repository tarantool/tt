package restore

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
)

// TestPlanTopologyMatchesByNameNotUUID is the sanctioned restore target: a
// freshly deployed cluster with the historical composition. Every UUID in the
// backup belongs to a deployment that no longer exists, and the configuration
// carries none at all, so the comparison has nothing but instance names to go on.
func TestPlanTopologyMatchesByNameNotUUID(t *testing.T) {
	for _, generation := range []string{"deploy-1", "deploy-2"} {
		t.Run(generation, func(t *testing.T) {
			f := newPlanFixture(t)
			f.addBackup(clusterBackup{
				id: fullBackupID, base: fullBackupID,
				backupType: backup.BackupTypeFull, createdAt: 200,
				shards: []shardBackup{
					{
						replicaset: shardA, master: masterOfA, generation: generation,
						replicaID: replicaOfA, vclockEnd: 1000,
						points: []backup.RecoveryPoint{
							recoveryPointAt("p1", replicaOfA, 500, 110),
							recoveryPointAt("p2", replicaOfA, 600, 210),
						},
					},
					{
						replicaset: shardB, master: masterOfB, generation: generation,
						replicaID: replicaOfB, vclockEnd: 900,
						points: []backup.RecoveryPoint{
							recoveryPointAt("p1", replicaOfB, 400, 111),
							recoveryPointAt("p2", replicaOfB, 450, 211),
						},
					},
				},
			})

			result := f.plan(150, masterOnlyCluster())

			require.Equal(t, StatusOK, result.Status)
			require.Nil(t, result.TopologyDiff)
			require.Empty(t, result.Warnings)
		})
	}
}

// TestPlanTopologyExtraReplicaset covers a cluster that has grown a shard the
// backup knows nothing about: restoring into it would leave that shard on
// whatever it holds now, silently mixed with restored data.
func TestPlanTopologyExtraReplicaset(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	current := configuredCluster(
		configuredReplicaset("storage-a", masterOfA),
		configuredReplicaset("storage-b", masterOfB),
		configuredReplicaset("storage-c", masterOfC),
	)

	result := f.plan(550, current)

	require.Equal(t, StatusTopologyMismatch, result.Status)
	require.Equal(t, "the topology of the recovery point does not match the cluster config",
		result.Reason)
	require.Nil(t, result.RecoveryPoint)
	require.Empty(t, result.DownloadPlan)

	require.NotNil(t, result.TopologyDiff)
	require.Equal(t, []ReplicasetDiff{{
		Name:      "storage-c",
		Instances: []string{masterOfC},
	}}, result.TopologyDiff.ExtraReplicasets)
	require.Empty(t, result.TopologyDiff.MissingReplicasets)
	require.Empty(t, result.TopologyDiff.AmbiguousReplicasets)

	// No point of this chain was taken on a topology carrying the third
	// replicaset, so there is nothing to offer instead.
	require.Nil(t, result.NearestSafe)

	require.Empty(t, f.downloaded(), "a blocked plan must not download anything")
}

// TestPlanTopologyMissingReplicaset covers a backed-up replicaset the
// configuration has no home for: its archive would have nowhere to go.
func TestPlanTopologyMissingReplicaset(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	result := f.plan(550, configuredCluster(configuredReplicaset("storage-a", masterOfA)))

	require.Equal(t, StatusTopologyMismatch, result.Status)
	require.NotNil(t, result.TopologyDiff)

	// The diff names the replicaset by what each side knows it as: the backup
	// has a UUID, the configuration a name, and both know the instance names.
	require.Equal(t, []ReplicasetDiff{{
		ReplicasetUUID: shardB,
		Instances:      []string{masterOfB},
	}}, result.TopologyDiff.MissingReplicasets)
	require.Empty(t, result.TopologyDiff.ExtraReplicasets)

	require.Empty(t, f.downloaded())
}

// TestPlanTopologyAmbiguousReplicaset covers a replicaset that was split in
// two: both halves share instance names with it, and picking either would be a
// guess about where the data belongs.
func TestPlanTopologyAmbiguousReplicaset(t *testing.T) {
	f := newPlanFixture(t)
	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			{
				replicaset: shardA, master: masterOfA, generation: "deploy-1",
				members:   []string{masterOfA, secondOfA},
				replicaID: replicaOfA, vclockEnd: 1000,
				points: []backup.RecoveryPoint{
					recoveryPointAt("p1", replicaOfA, 500, 110),
					recoveryPointAt("p2", replicaOfA, 600, 210),
				},
			},
		},
	})

	current := configuredCluster(
		configuredReplicaset("storage-a-left", masterOfA),
		configuredReplicaset("storage-a-right", secondOfA),
	)

	result := f.plan(150, current)

	require.Equal(t, StatusTopologyMismatch, result.Status)
	require.NotNil(t, result.TopologyDiff)
	require.Equal(t, []ReplicasetDiff{{
		ReplicasetUUID: shardA,
		Instances:      []string{masterOfA, secondOfA},
		Candidates:     []string{"storage-a-left", "storage-a-right"},
	}}, result.TopologyDiff.AmbiguousReplicasets)

	// Neither half was claimed, so both are also reported as configured
	// replicasets nothing in the backup maps onto.
	require.Equal(t, []ReplicasetDiff{
		{Name: "storage-a-left", Instances: []string{masterOfA}},
		{Name: "storage-a-right", Instances: []string{secondOfA}},
	}, result.TopologyDiff.ExtraReplicasets)

	require.Empty(t, f.downloaded())
}

// TestPlanTopologyReplacedReplicaRejoins covers the usual companion of a
// restore: a replica that was replaced while the cluster was down. It replays
// nothing, is wiped and joins the restored node once it runs, so it neither
// blocks the plan nor is worth a warning - it is a step of the procedure, and
// the plan says so by naming it under rejoin.
func TestPlanTopologyReplacedReplicaRejoins(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	current := configuredCluster(
		configuredReplicaset("storage-a", masterOfA, "storage-a-003"),
		configuredReplicaset("storage-b", masterOfB),
	)

	result := f.plan(550, current)

	require.Equal(t, StatusOK, result.Status)
	require.Nil(t, result.TopologyDiff)
	require.Len(t, result.DownloadPlan, 2)
	require.Empty(t, result.Warnings)

	require.Equal(t, []string{"storage-a-003"}, result.RestoreTargets[shardA].Rejoin)
	require.Empty(t, result.RestoreTargets[shardB].Rejoin)
}

// TestPlanTopologyRejoinsEveryConfiguredReplica pins what a realistic
// configuration produces. A manifest's topology is built from the backup
// fragments - one per master - so no replica of any replicaset is in it, and
// none of them has to be: only the target replays archives, and every other
// member comes back by joining it. A successful plan says that and warns about
// nothing.
func TestPlanTopologyRejoinsEveryConfiguredReplica(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	result := f.plan(550, liveCluster())

	require.Equal(t, StatusOK, result.Status)
	require.Empty(t, result.Warnings)

	require.Equal(t, map[string]RestoreTarget{
		shardA: {
			InstanceName: masterOfA,
			PatchUUID:    instanceUUIDOf("deploy-1", masterOfA),
			Rejoin:       []string{secondOfA},
		},
		shardB: {
			InstanceName: masterOfB,
			PatchUUID:    instanceUUIDOf("deploy-1", masterOfB),
			Rejoin:       []string{"storage-b-002"},
		},
	}, result.RestoreTargets)
}

// TestPlanTargetsRejoinKeepsTheConfiguredOrder pins what the wipe list holds
// when a replicaset has more than one member to wipe, and that the names come
// out sorted rather than in the order the configuration happened to list them.
// An orchestrator diffing two plans reads this as a change otherwise.
func TestPlanTargetsRejoinKeepsTheConfiguredOrder(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	current := configuredCluster(
		configuredReplicaset("storage-a", "storage-a-004", masterOfA, secondOfA),
		configuredReplicaset("storage-b", masterOfB),
	)

	result := f.plan(550, current)

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, []string{secondOfA, "storage-a-004"},
		result.RestoreTargets[shardA].Rejoin)
	require.Empty(t, result.RestoreTargets[shardB].Rejoin)
}

// TestPlanWarnsAboutAnInstanceTheConfigDropped covers the one instance-level
// difference that is still worth reporting: the backup knew a member the
// configuration no longer has. It is the surviving half of the comparison, and
// unlike the other half it says something a wipe list cannot.
func TestPlanWarnsAboutAnInstanceTheConfigDropped(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	// The backup's own topology lists both members of A.
	for _, backupID := range []string{fullBackupID, incOneID, incTwoID} {
		manifest := f.storedManifest(backupID)
		manifest.Topology.Replicasets[shardA] = topologyOf(shardBackup{
			master: masterOfA, members: []string{masterOfA, secondOfA},
			generation: "deploy-1",
		})
		f.putManifest(manifest)
	}

	result := f.plan(550, masterOnlyCluster())

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, []string{
		"replicaset " + shardA + " (storage-a): " + secondOfA +
			" backed up but not in the cluster config",
	}, result.Warnings)
	require.Empty(t, result.RestoreTargets[shardA].Rejoin)
}

// TestPlanTargetsWithoutAClusterConfig checks what the command can still answer
// with no -c: the node to restore onto and its UUID come from the backup, so
// they are named either way. Only the list of members to wipe is missing, since
// the composition of the cluster being restored into is what -c carries.
func TestPlanTargetsWithoutAClusterConfig(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	result := f.plan(550, nil)

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, map[string]RestoreTarget{
		shardA: {InstanceName: masterOfA, PatchUUID: instanceUUIDOf("deploy-1", masterOfA)},
		shardB: {InstanceName: masterOfB, PatchUUID: instanceUUIDOf("deploy-1", masterOfB)},
	}, result.RestoreTargets)
}

// TestPlanTargetsFollowAMasterChange checks which manifest the target is read
// from. A replicaset backed up on one instance and then on another has two
// answers in one chain, and the right one is the last backup's: that is the
// manifest the point was found in, and its archive is the one being restored.
func TestPlanTargetsFollowAMasterChange(t *testing.T) {
	f := newPlanFixture(t)
	f.chainAcrossAMasterChange()

	current := configuredCluster(
		configuredReplicaset("storage-a", masterOfA, secondOfA),
		configuredReplicaset("storage-b", masterOfB),
	)

	// The last point of this chain is the one the promoted master took, so it
	// is also the last moment the storage covers.
	result := f.plan(510, current)

	require.Equal(t, StatusOK, result.Status)
	require.Equal(t, RestoreTarget{
		InstanceName: secondOfA,
		PatchUUID:    instanceUUIDOf("deploy-1", secondOfA),
		Rejoin:       []string{masterOfA},
	}, result.RestoreTargets[shardA])
}

// TestRestoreTargetsWithoutARecordedUUID covers a manifest that names the
// instance its archive was taken on but records no UUID for it. A manifest like
// that does not validate, so a stored one never reaches a plan - the guard is
// against a chain that grew laxer, and the point of it is that the target comes
// out without a patch_uuid rather than with an empty one. The JSON is where
// that matters, so it is asserted there too: an empty `--patch-uuid` means "do
// not patch" to `tt restore apply`, a different and silently wrong instruction.
func TestRestoreTargetsWithoutARecordedUUID(t *testing.T) {
	instances := map[string]*backup.ShardInstance{
		shardA: {InstanceName: masterOfA},
	}

	targets, warnings := restoreTargets(instances, map[string]ConfiguredReplicaset{
		shardA: {Name: "storage-a", Instances: []string{masterOfA, secondOfA}},
	})

	require.Equal(t, map[string]RestoreTarget{
		shardA: {InstanceName: masterOfA, Rejoin: []string{secondOfA}},
	}, targets)
	require.Equal(t, []string{
		"replicaset " + shardA + ": the backup records no instance uuid for " +
			masterOfA + ", so the plan carries no patch_uuid; restore onto " +
			"that same instance and omit --patch-uuid, whose headers already " +
			"carry it",
	}, warnings)

	document := asJSON(t, &PlanResult{RestoreTargets: targets})
	target := document["restore_targets"].(map[string]any)[shardA].(map[string]any)
	require.Equal(t, []string{"instance_name", "rejoin"}, keysOf(t, target))
}

// TestPlanTopologyBlocksAnUncoveredMaster covers a master the configuration no
// longer carries. The archive of a replicaset is the master's, so a plan that
// cannot place it would silently drop that replicaset's data.
//
// It takes a manifest whose topology lists more than the master to get here at
// all: with the master alone, an intersection that matched the replicaset has
// already established that the configuration carries it.
func TestPlanTopologyBlocksAnUncoveredMaster(t *testing.T) {
	f := newPlanFixture(t)
	f.addBackup(clusterBackup{
		id: fullBackupID, base: fullBackupID,
		backupType: backup.BackupTypeFull, createdAt: 200,
		shards: []shardBackup{
			{
				replicaset: shardA, master: masterOfA, generation: "deploy-1",
				members:   []string{masterOfA, secondOfA},
				replicaID: replicaOfA, vclockEnd: 1000,
				points: []backup.RecoveryPoint{
					recoveryPointAt("p1", replicaOfA, 500, 110),
					recoveryPointAt("p2", replicaOfA, 600, 210),
				},
			},
		},
	})

	// The replicaset is matched through the surviving replica, but the instance
	// the archive was taken on is gone from the configuration.
	result := f.plan(150, configuredCluster(configuredReplicaset("storage-a", secondOfA)))

	require.Equal(t, StatusTopologyMismatch, result.Status)
	require.NotNil(t, result.TopologyDiff)
	require.Equal(t, []MasterDiff{{
		ReplicasetUUID: shardA,
		Name:           "storage-a",
		MasterInstance: masterOfA,
	}}, result.TopologyDiff.UncoveredMasters)
	require.Empty(t, result.TopologyDiff.MissingReplicasets)
	require.Empty(t, result.TopologyDiff.ExtraReplicasets)

	// The master is the same on every point of a segment - a master change is
	// itself a topology boundary - so a neighbouring point would fail
	// identically and offering one would send the operator in a circle.
	require.Nil(t, result.NearestSafe)

	require.Empty(t, f.downloaded())
}

// TestPlanTopologyOldPointAgainstANewCluster covers a target time that lands
// before a resharding: the point is sound, but the cluster it would be restored
// into no longer looks like that. The nearest point whose topology does match
// is named, so the orchestrator has something to retry with.
func TestPlanTopologyOldPointAgainstANewCluster(t *testing.T) {
	f := newPlanFixture(t)
	f.chainAcrossAResharding()

	// The target lands inside the old topology's own window; the gap after its
	// last point belongs to the topology boundary, a different verdict.
	result := f.plan(150, configuredCluster(
		configuredReplicaset("storage-a", masterOfA),
		configuredReplicaset("storage-c", masterOfC),
	))

	require.Equal(t, StatusTopologyMismatch, result.Status)
	require.NotNil(t, result.TopologyDiff)
	require.Equal(t, []ReplicasetDiff{{
		ReplicasetUUID: shardB,
		Instances:      []string{masterOfB},
	}}, result.TopologyDiff.MissingReplicasets)
	require.Equal(t, []ReplicasetDiff{{
		Name:      "storage-c",
		Instances: []string{masterOfC},
	}}, result.TopologyDiff.ExtraReplicasets)

	require.NotNil(t, result.NearestSafe)
	require.Nil(t, result.NearestSafe.Before)
	require.Equal(t, time.Unix(510, 0).UTC(), *result.NearestSafe.After)

	require.Empty(t, f.downloaded())
}

// TestPlanWithoutAClusterConfigStillPlans covers a run with no -c: the check is
// worth having, but the storage is worth reading without a cluster config at
// hand, and the RFC's own step-1 example passes none.
func TestPlanWithoutAClusterConfigStillPlans(t *testing.T) {
	f := newPlanFixture(t)
	f.standardChain()

	result := f.plan(550, nil)

	require.Equal(t, StatusOK, result.Status)
	require.Nil(t, result.TopologyDiff)
	require.Equal(t, []string{noTopologyCheckWarning}, result.Warnings)
	require.Len(t, result.DownloadPlan, 2)
}

// TestPlanNeverContactsTheClusterBeingRestored guards the decision that the
// current topology comes from -c alone. The main scenario of this command is a
// cluster whose nodes are dead or crash-looping: a source that had to connect
// to them would pay a connect timeout per instance before the plan could even
// be printed, and a lost replica would read as a topology change that is not
// there.
func TestPlanNeverContactsTheClusterBeingRestored(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(PlanOpts{}))

	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, field.Name)
	}

	require.Equal(t, []string{"Storage", "TargetTime", "Dir", "Current"}, names,
		"a new PlanOpts field must not give the plan a way to reach the cluster")

	// Current is a plain description, not a connection: it carries no addresses
	// and nothing to dial.
	f := newPlanFixture(t)
	f.standardChain()

	require.Equal(t, StatusOK, f.plan(550, masterOnlyCluster()).Status)
}
