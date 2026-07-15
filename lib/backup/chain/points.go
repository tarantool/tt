package chain

import (
	"slices"
	"time"
)

// buildSegments partitions groups into time-ordered segments and records, for
// each, why it is separated from the previous one.
func buildSegments(groups []Group) []segment {
	var segments []segment
	for g := range groups {
		groupSegments := segmentsFromGroup(groups[g])
		if len(groupSegments) == 0 {
			continue
		}
		if len(segments) > 0 {
			groupSegments[0].gapBefore = gapBroken
		}
		segments = append(segments, groupSegments...)
	}

	slices.SortFunc(segments, func(a, b segment) int {
		return a.points[0].Timestamp.Compare(b.points[0].Timestamp)
	})
	return segments
}

// buildClusterPoints flattens segments into a single time-ordered point slice.
func buildClusterPoints(segments []segment) []ClusterPoint {
	var points []ClusterPoint
	for _, seg := range segments {
		points = append(points, seg.points...)
	}
	slices.SortFunc(points, func(a, b ClusterPoint) int {
		return a.Timestamp.Compare(b.Timestamp)
	})
	return points
}

// segmentsFromGroup splits one group into topology segments, carrying each
// segment's separation reason.
func segmentsFromGroup(group Group) []segment {
	runs := splitIntoRuns(group.Entries)

	var segments []segment
	pendingGap := gapNone
	for _, run := range runs {
		points := pointsFromSegment(run.entries)
		if len(points) == 0 {
			pendingGap = maxGap(pendingGap, run.gapBefore)
			continue
		}
		gap := run.gapBefore
		if len(segments) == 0 {
			// First point-bearing segment: only a carried-over gap matters.
			gap = pendingGap
		} else {
			gap = maxGap(gap, pendingGap)
		}
		segments = append(segments, segment{points: points, gapBefore: gap})
		pendingGap = gapNone
	}
	return segments
}

// maxGap returns the more severe gap: break outranks boundary outranks none.
func maxGap(a, b gapKind) gapKind {
	if a == gapBroken || b == gapBroken {
		return gapBroken
	}
	if a == gapTopology || b == gapTopology {
		return gapTopology
	}
	return gapNone
}

// run is a contiguous sequence of clean entries with uniform topology plus its
// separation reason from the previous run.
type run struct {
	entries   []*Entry
	gapBefore gapKind
}

// splitIntoRuns partitions ordered entries into runs of clean, same-topology
// entries.
func splitIntoRuns(entries []*Entry) []run {
	var runs []run
	var current []*Entry
	nextGap := gapNone

	flush := func() {
		if len(current) > 0 {
			runs = append(runs, run{entries: current, gapBefore: nextGap})
			current = nil
			nextGap = gapNone
		}
	}

	for _, entry := range entries {
		if len(entry.Problems) > 0 {
			flush()
			nextGap = gapBroken
			continue
		}
		if len(current) > 0 && !topologyEqual(current[len(current)-1].Manifest, entry.Manifest) {
			flush()
			nextGap = gapTopology
		}
		current = append(current, entry)
	}
	flush()
	return runs
}

// shardEntry is one recovery point's position and effective timestamp on a
// single replicaset.
type shardEntry struct {
	position  Position
	timestamp time.Time
}

// collectShardEntries maps each recovery point name to its first-seen shard
// entry per replicaset across the segment's entries.
func collectShardEntries(entries []*Entry) map[string]map[string]shardEntry {
	pointShards := make(map[string]map[string]shardEntry)
	for _, entry := range entries {
		for _, replicasetUUID := range replicasetUUIDs(entry.Manifest) {
			shard := entry.Manifest.Shards[replicasetUUID]
			if shard.Instance == nil {
				continue
			}
			for _, rp := range shard.Instance.Artifact.RecoveryPoints {
				name := rp.UUID
				if _, ok := pointShards[name]; !ok {
					pointShards[name] = make(map[string]shardEntry)
				}
				if _, seen := pointShards[name][replicasetUUID]; !seen {
					pointShards[name][replicasetUUID] = shardEntry{
						position:  Position{ReplicaID: rp.ReplicaID, LSN: rp.LSN},
						timestamp: time.Unix(rp.Timestamp, 0).UTC(),
					}
				}
			}
		}
	}
	return pointShards
}

// pointsFromSegment collects cluster points from one topology-homogeneous
// segment: a name becomes a point only when present on every replicaset in the
// segment's topology.
func pointsFromSegment(entries []*Entry) []ClusterPoint {
	if len(entries) == 0 {
		return nil
	}

	uuids := replicasetUUIDs(entries[0].Manifest)
	required := len(uuids)
	segmentTopology := entries[0].Manifest.Topology
	pointShards := collectShardEntries(entries)

	var points []ClusterPoint
	for name, byReplicaset := range pointShards {
		if len(byReplicaset) < required {
			continue
		}
		shards := make(map[string]Position, required)
		minTS := time.Time{}
		for _, replicasetUUID := range uuids {
			se := byReplicaset[replicasetUUID]
			shards[replicasetUUID] = se.position
			if minTS.IsZero() || se.timestamp.Before(minTS) {
				minTS = se.timestamp
			}
		}
		points = append(points, ClusterPoint{
			Name:      name,
			Timestamp: minTS,
			Topology:  segmentTopology,
			Shards:    shards,
		})
	}

	slices.SortFunc(points, func(a, b ClusterPoint) int {
		return a.Timestamp.Compare(b.Timestamp)
	})
	return points
}
