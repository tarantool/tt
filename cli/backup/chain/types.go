// Package chain builds recovery chains from backup manifests.
package chain

import (
	"fmt"
	"slices"
	"time"

	"github.com/tarantool/tt/cli/backup"
)

// Selection narrows the replicasets a cluster point is stitched over. It is
// expressed as a set of instance names because a manifest knows instances and
// replicaset UUIDs, while the operator names replicasets the way the cluster
// config does. A nil *Selection covers every replicaset.
type Selection struct {
	// instances holds the selected instance names.
	instances map[string]bool
}

// SelectInstances builds a selection from instance names. A replicaset is
// selected when at least one of its topology instances carries one of them.
func SelectInstances(names []string) *Selection {
	instances := make(map[string]bool, len(names))
	for _, name := range names {
		instances[name] = true
	}

	return &Selection{instances: instances}
}

// uuids returns the sorted UUIDs of the replicasets this selection covers in
// the given topology. A nil selection covers all of them.
func (s *Selection) uuids(topology backup.Topology) []string {
	uuids := make([]string, 0, len(topology.Replicasets))

	for uuid, instances := range topology.Replicasets {
		if s == nil || s.covers(instances) {
			uuids = append(uuids, uuid)
		}
	}

	slices.Sort(uuids)

	return uuids
}

// covers reports whether the selection names any of the given instances. An
// instance with no name cannot be selected: the name is the only handle the
// operator has on it.
func (s *Selection) covers(instances []backup.TopologyInstance) bool {
	for _, instance := range instances {
		if instance.InstanceName != "" && s.instances[instance.InstanceName] {
			return true
		}
	}

	return false
}

// Status describes the result of resolving a requested recovery time.
type Status int

const (
	StatusOK Status = iota
	StatusTopologyBoundary
	StatusNoRecoveryPoint
	StatusChainBroken
	StatusOutOfRange
)

// ProblemKind identifies a manifest's place in a broken chain.
type ProblemKind int

const (
	ProblemOrphan ProblemKind = iota
	ProblemFork
	ProblemVclockMismatch
	ProblemInvalidManifest
)

// Problem is a chain problem attached to one manifest.
type Problem struct {
	// Kind classifies the problem.
	Kind ProblemKind
	// BackupID identifies the manifest whose recovery path is affected.
	BackupID string
	// Inherited reports that the problem originated in an ancestor.
	Inherited bool
	// Detail identifies the missing, conflicting, or mismatched link.
	Detail string
}

// Entry is one manifest and the chain problems that make it unusable.
type Entry struct {
	// Manifest is the original cluster manifest.
	Manifest *backup.ClusterManifest
	// Problems is empty only when this manifest is usable for recovery.
	Problems []*Problem
}

// Group is a cascade-deletion unit rooted at one full backup.
type Group struct {
	// Entries starts with the full backup and follows previous_backup_id.
	Entries []*Entry
}

// Position is a recovery position inside one replicaset.
type Position struct {
	// ReplicaID is the source instance identifier in the replicaset.
	ReplicaID uint32
	// LSN is the xlog position on that replica.
	LSN uint64
}

// String renders the position the way tt restore apply takes it on the command
// line, so an error naming one can be acted on directly.
func (p Position) String() string {
	return fmt.Sprintf("{replica_id: %d, lsn: %d}", p.ReplicaID, p.LSN)
}

// ClusterPoint joins equally named points from every replicaset in a segment.
type ClusterPoint struct {
	// Name is the manager-generated common point name.
	Name string
	// Timestamp is the earliest timestamp among the shard points.
	Timestamp time.Time
	// Topology of the segment this point was stitched in, constant within it.
	// It lists every backed-up replicaset, including those a selection left out
	// of Shards; a consumer that cares only about the restored ones narrows it
	// by the keys of Shards. Consumers compare it against the live cluster;
	// chain does not.
	Topology backup.Topology
	// Shards maps a replicaset UUID to its position at this point.
	Shards map[string]Position
}

// Resolution is the result of resolving a requested recovery time.
type Resolution struct {
	// Status explains whether Point can be used.
	Status Status
	// Point is selected for StatusOK, or the last healthy point before a break.
	Point *ClusterPoint
	// Before and After are nearest points around an unresolved target time.
	Before *ClusterPoint
	After  *ClusterPoint
}

// ShardPlan is an ordered backup path for one replicaset.
type ShardPlan struct {
	// Backups starts with a full backup and continues with incrementals.
	Backups []*backup.ClusterManifest
	// TrimTo limits the final incremental to the requested position.
	TrimTo *Position
}

// Plan describes all backups required to recover one cluster point.
type Plan struct {
	// Point is the recovery target.
	Point ClusterPoint
	// Shards maps a replicaset UUID to the backup path needed for it.
	Shards map[string]ShardPlan
}
