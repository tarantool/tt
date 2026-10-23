package chain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tarantool/tt/v3/cli/backup"
)

var (
	errAPIClusterPoint     = errors.New("cluster point")
	errAPIBackup           = errors.New("backup")
	errAPINoManifestCovers = errors.New("no manifest covers")
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
func (c *Chain) Resolve(target time.Time) Resolution {
	if len(c.points) == 0 {
		if c.covers(target) {
			return Resolution{Status: StatusNoRecoveryPoint, Point: nil, Before: nil, After: nil}
		}

		return Resolution{Status: StatusOutOfRange, Point: nil, Before: nil, After: nil}
	}

	first := c.points[0]
	last := c.points[len(c.points)-1]

	// Before the first point the two failures look alike but are not: a time
	// earlier than every manifest is out of what this storage holds at all,
	// while a time inside the covered window merely has no point to land on.
	// The operator's next move differs - reach for another storage, or take the
	// neighbouring point offered here - so the two statuses stay apart.
	if target.Before(first.Timestamp) {
		if !c.covers(target) {
			return Resolution{Status: StatusOutOfRange, After: &first, Point: nil, Before: nil}
		}

		return Resolution{Status: StatusNoRecoveryPoint, After: &first, Point: nil, Before: nil}
	}

	if target.After(last.Timestamp) {
		return Resolution{Status: StatusOutOfRange, Before: &last, After: nil, Point: nil}
	}

	// t is within overall coverage. Scan segments in time order: t either lands
	// in the gap right after a segment (report it) or is covered by one.
	for index := range c.segments {
		seg := &c.segments[index]
		segLast := seg.points[len(seg.points)-1]

		if target.After(segLast.Timestamp) {
			// A later segment exists (t is within coverage); if it starts after t,
			// t is in the gap between them.
			next := &c.segments[index+1]
			if target.Before(next.points[0].Timestamp) {
				return gapResolution(next.gapBefore, &segLast, &next.points[0])
			}

			continue
		}

		// A point ≤ t exists in this segment: pick the latest such one.
		var candidate *ClusterPoint

		for j := range seg.points {
			p := &seg.points[j]
			if !p.Timestamp.After(target) {
				candidate = p
			}
		}

		return Resolution{Status: StatusOK, Point: candidate, Before: nil, After: nil}
	}

	// Unreachable: the loop always returns for a t within overall coverage.
	return Resolution{Status: StatusOK, Point: &last, Before: nil, After: nil}
}

// gapResolution builds the resolution for a t that fell into a segment gap. A
// chain break also offers the last point before the break as Point.
func gapResolution(gap gapKind, before, after *ClusterPoint) Resolution {
	res := Resolution{Status: gap.status(), Before: before, After: after, Point: nil}
	if gap == gapBroken {
		res.Point = before
	}

	return res
}

// PlanFor builds per-replicaset backup paths for a cluster recovery point, or
// errors if p is not a point produced by this chain.
func (c *Chain) PlanFor(point ClusterPoint) (Plan, error) {
	entries := c.pointEntries(point.Name)
	if len(entries) == 0 {
		return Plan{}, fmt.Errorf("%w %q not found in chain", errAPIClusterPoint, point.Name)
	}

	index := c.byID
	shards := make(map[string]ShardPlan, len(point.Shards))

	for replicasetUUID, pos := range point.Shards {
		sp, err := shardPlan(replicasetUUID, pos, point, index, entries)
		if err != nil {
			return Plan{}, fmt.Errorf("build plan for replicaset %q: %w", replicasetUUID, err)
		}

		shards[replicasetUUID] = sp
	}

	return Plan{Point: point, Shards: shards}, nil
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
// label without complaint, and nothing downstream enforces uniqueness. So the
// entry must carry the label at exactly that position, and an entry that
// covers the position without doing so means the manifests disagree with each
// other.
//
// The entries are those of the segments the point was stitched in, never the
// whole storage. A full backup is bounded only from above - see
// coversPosition - while the core lists in it just the points past its
// checkpoint. A full of another chain whose checkpoint is already past the
// point would therefore cover a position it carries nothing for, and the
// answer would depend on how backup ids happen to sort. Within the point's own
// segment no such full exists: a point before its full's checkpoint is not in
// that chain at all.
//
// Entries are walked in the order given, so the answer does not depend on map
// iteration order.
func findSourceEntry(
	replicasetUUID string,
	pointName string,
	pos Position,
	entries []*Entry,
) (*Entry, error) {
	for _, entry := range entries {
		shard := entry.Manifest.Shards[replicasetUUID]
		if shard.Instance == nil {
			continue
		}

		if !coversPosition(shard.Instance, pos) {
			continue
		}

		if carriesPoint(shard.Instance, pointName, pos) {
			return entry, nil
		}

		return nil, fmt.Errorf(
			"%w %q covers %s for replicaset %q but carries no point %q there", errAPIBackup,
			entry.Manifest.BackupID, pos, replicasetUUID, pointName,
		)
	}

	return nil, fmt.Errorf(
		"%w %s for replicaset %q, wanted for point %q", errAPINoManifestCovers,
		pos, replicasetUUID, pointName,
	)
}

// carriesPoint reports whether the shard lists a recovery point with the given
// label at exactly pos.
func carriesPoint(instance *backup.ShardInstance, label string, pos Position) bool {
	for _, rp := range instance.Artifact.RecoveryPoints {
		if rp.Label == label && rp.ReplicaID == pos.ReplicaID && rp.LSN == pos.LSN {
			return true
		}
	}

	return false
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
