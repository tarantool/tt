package backup

import (
	"fmt"
	"slices"
	"strings"
)

// LiveInstance is one instance in the live cluster topology.
type LiveInstance struct {
	InstanceUUID string `json:"instance_uuid"`
	InstanceName string `json:"instance_name"`
	Hostname     string `json:"hostname"`
	Mode         string `json:"mode"`
	Status       string `json:"status"`
}

// LiveTopology is the current cluster topology discovered from live nodes.
type LiveTopology struct {
	Replicasets map[string][]LiveInstance `json:"replicasets"`
}

// ReplicasetPlan describes the backup plan for one replicaset.
type ReplicasetPlan struct {
	MasterInstanceUUID string `json:"master_instance_uuid"`
	MasterInstanceName string `json:"master_instance_name"`
	FromVclock         Vclock `json:"from_vclock,omitempty"`
}

// BackupPlan is the output of the plan command.
type BackupPlan struct {
	Type             BackupType                `json:"mode"`
	Replicasets      map[string]ReplicasetPlan `json:"replicasets,omitempty"`
	PreviousBackupID BackupID                  `json:"previous_backup_id,omitempty"`
	BaseFullBackupID BackupID                  `json:"base_full_backup_id,omitempty"`
}

var (
	ErrNoBackups = fmt.Errorf(
		"no backups found in storage; use --target=full for the first backup",
	)

	ErrShardDegraded = fmt.Errorf(
		"latest backup is degraded; use --target=full to start a new chain",
	)

	ErrTopologyChanged = fmt.Errorf(
		"topology changed since the last backup; use --target=full to start a new chain",
	)

	ErrMasterChanged = fmt.Errorf(
		"master changed since the last backup; use --target=full to start a new chain",
	)

	ErrNoMaster = fmt.Errorf("no RW master found in replicaset")
)

// Plan builds a backup plan from the latest manifest and the live topology.
func Plan(target BackupType, latest *ClusterManifest, live *LiveTopology) (*BackupPlan, error) {
	switch target {
	case BackupTypeFull:
		return planFull(live) //nolint:wrapcheck
	case BackupTypeIncremental:
		return planIncremental(latest, live) //nolint:wrapcheck
	default:
		return nil, fmt.Errorf("unknown target %q", target)
	}
}

// planFull selects the first RW master per replicaset from the live topology.
func planFull(live *LiveTopology) (*BackupPlan, error) {
	if live == nil {
		return nil, fmt.Errorf("live topology is required for a full backup plan")
	}

	replicasets := make(map[string]ReplicasetPlan, len(live.Replicasets))
	for rsUUID, instances := range live.Replicasets {
		idx := slices.IndexFunc(instances, func(inst LiveInstance) bool {
			return inst.Mode == "rw"
		})

		if idx < 0 {
			return nil, fmt.Errorf("replicaset %q: %w", rsUUID, ErrNoMaster)
		}

		master := instances[idx]

		replicasets[rsUUID] = ReplicasetPlan{
			MasterInstanceUUID: master.InstanceUUID,
			MasterInstanceName: master.InstanceName,
		}
	}

	return &BackupPlan{Type: BackupTypeFull, Replicasets: replicasets}, nil
}

// planIncremental validates the latest manifest against the live topology.
func planIncremental(latest *ClusterManifest, live *LiveTopology) (*BackupPlan, error) {
	if latest == nil {
		return nil, ErrNoBackups
	}
	if live == nil {
		return nil, fmt.Errorf("live topology is required for an incremental backup plan")
	}

	// Every shard must have a backed-up instance.
	for rsUUID, shard := range latest.Shards {
		if shard.Instance == nil {
			return nil, fmt.Errorf(
				"%w: replicaset %q has no instance in backup %q",
				ErrShardDegraded, rsUUID, latest.BackupID)
		}
	}

	// The replicaset set must match exactly.
	if diff := diffReplicasets(latest, live); diff != "" {
		return nil, fmt.Errorf("%w: %s", ErrTopologyChanged, diff)
	}

	// The backed-up instance must still be RW.
	replicasets := make(map[string]ReplicasetPlan, len(latest.Shards))
	for rsUUID, shard := range latest.Shards {
		inst := shard.Instance
		liveIdx := slices.IndexFunc(live.Replicasets[rsUUID], func(li LiveInstance) bool {
			return li.InstanceUUID == inst.InstanceUUID
		})
		if liveIdx < 0 {
			return nil, fmt.Errorf(
				"%w: replicaset %q instance %q not found in live topology",
				ErrMasterChanged, rsUUID, inst.InstanceUUID)
		}

		liveInst := live.Replicasets[rsUUID][liveIdx]

		if liveInst.Mode != "rw" {
			return nil, fmt.Errorf(
				"%w: replicaset %q instance %q is no longer RW (mode=%q)",
				ErrMasterChanged, rsUUID, inst.InstanceUUID, liveInst.Mode)
		}

		replicasets[rsUUID] = ReplicasetPlan{
			MasterInstanceUUID: inst.InstanceUUID,
			MasterInstanceName: inst.InstanceName,
			FromVclock:         inst.VclockEnd,
		}
	}

	return &BackupPlan{
		Type:             BackupTypeIncremental,
		Replicasets:      replicasets,
		PreviousBackupID: latest.BackupID,
		BaseFullBackupID: latest.BaseFullBackupID,
	}, nil
}

// diffReplicasets returns a human-readable diff of replicaset UUIDs between a
// manifest and the live topology, or "" if they match.
func diffReplicasets(latest *ClusterManifest, live *LiveTopology) string {
	manifestRS := sortedKeys(manifestTopologyToMap(latest))
	liveRS := sortedKeys(live.Replicasets)

	if slices.Equal(manifestRS, liveRS) {
		return ""
	}

	removed, added := setDiff(manifestRS, liveRS)

	var parts []string
	if len(removed) > 0 {
		parts = append(parts, fmt.Sprintf("removed: %s", strings.Join(removed, ", ")))
	}

	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("added: %s", strings.Join(added, ", ")))
	}

	return strings.Join(parts, "; ")
}

// manifestTopologyToMap returns replicaset UUIDs from a manifest as a set.
func manifestTopologyToMap(m *ClusterManifest) map[string]bool {
	set := make(map[string]bool, len(m.Topology.Replicasets))
	for uuid := range m.Topology.Replicasets {
		set[uuid] = true
	}

	return set
}

// sortedKeys returns sorted keys of a map with string keys.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)
	return keys
}

// setDiff returns elements in a but not b (removed), and in b but not a (added).
func setDiff(a, b []string) (removed, added []string) {
	setB := sliceToSet(b)
	setA := sliceToSet(a)

	for _, v := range a {
		if !setB[v] {
			removed = append(removed, v)
		}
	}

	for _, v := range b {
		if !setA[v] {
			added = append(added, v)
		}
	}

	return removed, added
}

// sliceToSet converts a string slice to a set.
func sliceToSet(s []string) map[string]bool {
	set := make(map[string]bool, len(s))
	for _, v := range s {
		set[v] = true
	}

	return set
}
