package chain

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/tarantool/tt/cli/backup"
)

// String returns the status name.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "ok"
	case StatusTopologyBoundary:
		return "topology_boundary"
	case StatusNoRecoveryPoint:
		return "no_recovery_point"
	case StatusChainBroken:
		return "chain_broken"
	case StatusOutOfRange:
		return "out_of_range"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes a status as its string.
func (s Status) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(s.String())
	if err != nil {
		return nil, fmt.Errorf("marshal status: %w", err)
	}

	return data, nil
}

// Resolve maps a requested recovery time to a cluster recovery point.
func (c *Chain) Resolve(t time.Time) Resolution {
	if len(c.points) == 0 {
		if c.covers(t) {
			return Resolution{Status: StatusNoRecoveryPoint}
		}

		return Resolution{Status: StatusOutOfRange}
	}

	first := c.points[0]
	last := c.points[len(c.points)-1]

	// Before the first point the two failures look alike but are not: a time
	// earlier than every manifest is out of what this storage holds at all,
	// while a time inside the covered window merely has no point to land on.
	// The operator's next move differs - reach for another storage, or take the
	// neighbouring point offered here - so the two statuses stay apart.
	if t.Before(first.Timestamp) {
		if !c.covers(t) {
			return Resolution{Status: StatusOutOfRange, After: &first}
		}

		return Resolution{Status: StatusNoRecoveryPoint, After: &first}
	}

	if t.After(last.Timestamp) {
		return Resolution{Status: StatusOutOfRange, Before: &last}
	}

	// t is within overall coverage. Scan segments in time order: t either lands
	// in the gap right after a segment (report it) or is covered by one.
	for i := range c.segments {
		seg := &c.segments[i]
		segLast := seg.points[len(seg.points)-1]
		if t.After(segLast.Timestamp) {
			// A later segment exists (t is within coverage); if it starts after t,
			// t is in the gap between them.
			next := &c.segments[i+1]
			if t.Before(next.points[0].Timestamp) {
				return gapResolution(next.gapBefore, &segLast, &next.points[0])
			}
			continue
		}

		// A point ≤ t exists in this segment: pick the latest such one.
		var candidate *ClusterPoint
		for j := range seg.points {
			p := &seg.points[j]
			if !p.Timestamp.After(t) {
				candidate = p
			}
		}
		return Resolution{Status: StatusOK, Point: candidate}
	}

	// Unreachable: the loop always returns for a t within overall coverage.
	return Resolution{Status: StatusOK, Point: &last}
}

// gapResolution builds the resolution for a t that fell into a segment gap. A
// chain break also offers the last point before the break as Point.
func gapResolution(gap gapKind, before, after *ClusterPoint) Resolution {
	res := Resolution{Status: gap.status(), Before: before, After: after}
	if gap == gapBroken {
		res.Point = before
	}

	return res
}

// PlanFor builds per-replicaset backup paths for a cluster recovery point, or
// errors if p is not a point produced by this chain.
func (c *Chain) PlanFor(p ClusterPoint) (Plan, error) {
	found := false
	for i := range c.points {
		if c.points[i].Name == p.Name {
			found = true
			break
		}
	}

	if !found {
		return Plan{}, fmt.Errorf("cluster point %q not found in chain", p.Name)
	}

	index := c.byID
	ordered := c.orderedEntries()
	shards := make(map[string]ShardPlan, len(p.Shards))
	for replicasetUUID, pos := range p.Shards {
		sp, err := shardPlan(replicasetUUID, pos, p, index, ordered)
		if err != nil {
			return Plan{}, fmt.Errorf("build plan for replicaset %q: %w", replicasetUUID, err)
		}
		shards[replicasetUUID] = sp
	}

	return Plan{Point: p, Shards: shards}, nil
}

// shardPlan builds the ordered backup path (full first, incrementals after) for
// one replicaset by walking previous_backup_id back from the manifest carrying
// the point.
func shardPlan(
	replicasetUUID string,
	pos Position,
	point ClusterPoint,
	index map[backup.BackupID]*Entry,
	ordered []*Entry,
) (ShardPlan, error) {
	source, err := findSourceEntry(replicasetUUID, point.Name, pos, ordered)
	if err != nil {
		return ShardPlan{}, fmt.Errorf("find source entry: %w", err)
	}

	// Walk back via previous_backup_id, keeping only manifests carrying this shard.
	var reversed []*backup.ClusterManifest
	visited := make(map[backup.BackupID]bool)
	current := source

	for {
		if current.Manifest.Shards[replicasetUUID].Instance != nil {
			reversed = append(reversed, current.Manifest)
		}
		prevID := backup.BackupID(current.Manifest.PreviousBackupID)
		if prevID == "" || visited[current.Manifest.BackupID] {
			break
		}
		visited[current.Manifest.BackupID] = true
		parent, ok := index[prevID]
		if !ok {
			break
		}
		current = parent
	}

	// Reverse to chronological order: full backup first.
	backups := make([]*backup.ClusterManifest, len(reversed))
	for i, m := range reversed {
		backups[len(reversed)-1-i] = m
	}

	var trimTo *Position

	shard := source.Manifest.Shards[replicasetUUID]
	if shard.Instance != nil {
		vclockEnd := shard.Instance.VclockEnd
		if endLSN, ok := vclockEnd[pos.ReplicaID]; !ok || pos.LSN < endLSN {
			trimTo = &pos
		}
	}

	return ShardPlan{Backups: backups, TrimTo: trimTo}, nil
}

// findSourceEntry returns the backup the recovery position falls inside, for
// the given replicaset.
//
// The position selects the entry, not the point's label. The caller trims
// against this entry's vclock_end, so an entry that does not contain the
// position yields either no trim at all or a trim inside the wrong backup -
// both of which restore an instance that looks healthy at the wrong state.
// Labels cannot carry that weight: the core creates two points with the same
// label without complaint, and nothing downstream enforces uniqueness. The
// label is still checked, because an entry that covers the position without
// carrying the label means the manifests disagree with each other.
//
// Entries are walked in chain order, oldest first, so the answer does not
// depend on map iteration order.
func findSourceEntry(
	replicasetUUID string,
	pointName string,
	pos Position,
	ordered []*Entry,
) (*Entry, error) {
	for _, entry := range ordered {
		shard := entry.Manifest.Shards[replicasetUUID]
		if shard.Instance == nil {
			continue
		}

		if !coversPosition(shard.Instance, pos) {
			continue
		}

		for _, rp := range shard.Instance.Artifact.RecoveryPoints {
			if rp.Label == pointName {
				return entry, nil
			}
		}

		return nil, fmt.Errorf(
			"backup %q covers %s for replicaset %q but carries no point %q",
			entry.Manifest.BackupID, pos, replicasetUUID, pointName,
		)
	}

	return nil, fmt.Errorf(
		"no manifest covers %s for replicaset %q, wanted for point %q",
		pos, replicasetUUID, pointName,
	)
}

// coversPosition reports whether the shard's backup range contains pos. A full
// backup has no begin vclock - the core reports none - so only the end bounds
// it.
func coversPosition(instance *backup.ShardInstance, pos Position) bool {
	endLSN, ok := instance.VclockEnd[pos.ReplicaID]
	if !ok || pos.LSN > endLSN {
		return false
	}

	beginLSN, ok := instance.VclockBegin[pos.ReplicaID]

	return !ok || pos.LSN > beginLSN
}
