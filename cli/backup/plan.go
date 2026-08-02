package backup

import (
	"fmt"
	"slices"
	"strings"
)

// Instance modes and statuses as cluster discovery reports them.
const (
	modeRW          = "rw"
	modeUnknown     = "unknown"
	statusReachable = "OK"
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

	// A shard nobody could reach says nothing about where the master is, so
	// this one deliberately does not recommend --target=full: answering a
	// restart with a new full chain is not reversible.
	ErrReplicasetUnreachable = fmt.Errorf(
		"replicaset is not reachable; retry once the instance is back",
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
			return inst.Mode == modeRW
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
		if err := checkMasterUsable(rsUUID, inst, live.Replicasets[rsUUID]); err != nil {
			return nil, err //nolint:wrapcheck
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

// checkMasterUsable reports why the instance backed up last time cannot serve
// the next increment, or nil if it still can.
//
// Discovery lists an instance it could not reach as part of the topology with
// mode "unknown", so the mode alone cannot tell a down master from a demoted
// one. The master has really moved only once another instance holds the RW
// role; until then the replicaset merely has no live view, and answering a
// transient outage with a whole new full chain cannot be undone.
func checkMasterUsable(rsUUID string, inst *ShardInstance, live []LiveInstance) error {
	var master *LiveInstance
	if idx := slices.IndexFunc(live, func(li LiveInstance) bool {
		return li.InstanceUUID == inst.InstanceUUID
	}); idx >= 0 {
		master = &live[idx]
	}

	reachable := master != nil && isReachable(*master)
	failedOver := slices.ContainsFunc(live, func(li LiveInstance) bool {
		return isReachable(li) && li.Mode == modeRW
	})

	switch {
	case reachable && master.Mode == modeRW:
		return nil
	case reachable:
		return fmt.Errorf(
			"%w: replicaset %q instance %q is no longer RW (mode=%q)",
			ErrMasterChanged, rsUUID, inst.InstanceUUID, master.Mode)
	case master == nil && failedOver:
		return fmt.Errorf(
			"%w: replicaset %q instance %q not found in live topology",
			ErrMasterChanged, rsUUID, inst.InstanceUUID)
	case failedOver:
		return fmt.Errorf(
			"%w: replicaset %q instance %q is not reachable, another instance is RW now",
			ErrMasterChanged, rsUUID, inst.InstanceUUID)
	case master == nil:
		return fmt.Errorf(
			"%w: replicaset %q has no reachable instance",
			ErrReplicasetUnreachable, rsUUID)
	default:
		return fmt.Errorf(
			"%w: replicaset %q instance %q is not reachable (mode=%q, status=%q)",
			ErrReplicasetUnreachable, rsUUID, inst.InstanceUUID,
			master.Mode, master.Status)
	}
}

// isReachable reports whether the live view of inst was taken from the
// instance itself: discovery reports a node it could not reach with status
// "not reachable" and mode "unknown". A caller that fills neither field is
// taken at its word.
func isReachable(inst LiveInstance) bool {
	switch {
	case inst.Status != "" && inst.Status != statusReachable:
		return false
	case inst.Mode == modeUnknown:
		return false
	default:
		return true
	}
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
