package chain

import (
	"cmp"
	"slices"

	"github.com/tarantool/tt/lib/backup"
)

// Chain is the in-memory view of all manifests in one storage.
type Chain struct {
	// groups are ordered from oldest full backup to newest.
	groups []Group
	// byID indexes group entries by backup_id for linking and PlanFor traversal.
	byID map[backup.BackupID]*Entry
	// points are ordered by their effective recovery timestamp.
	points []ClusterPoint
	// segments are time-ordered runs of points; the gap between two of them
	// carries why the chain is not stitched across it, which Resolve reports.
	segments []segment
}

// segment is a contiguous run of cluster points with uniform topology plus the
// reason it is separated from the previous segment.
type segment struct {
	points    []ClusterPoint
	gapBefore gapKind
}

// gapKind classifies the separation between two adjacent segments.
type gapKind int

const (
	gapNone     gapKind = iota // first segment: no predecessor
	gapTopology                // composition or master changed
	gapBroken                  // problematic entry or unrelated full backup
)

// status maps a gap to the resolve status reported when a target time falls in it.
func (g gapKind) status() Status {
	if g == gapTopology {
		return StatusTopologyBoundary
	}
	return StatusChainBroken
}

// Groups returns backup groups from oldest full backup to newest.
func (c *Chain) Groups() []Group {
	groups := make([]Group, len(c.groups))
	for i, group := range c.groups {
		entries := make([]*Entry, len(group.Entries))
		for j, entry := range group.Entries {
			entries[j] = copyEntry(entry)
		}
		groups[i].Entries = entries
	}
	return groups
}

// Latest returns the last entry in chain order, regardless of its Problems.
func (c *Chain) Latest() *Entry {
	var latest *Entry
	for _, entry := range c.byID {
		if latest == nil || compareEntries(latest, entry) < 0 {
			latest = entry
		}
	}
	if latest == nil {
		return nil
	}
	return copyEntry(latest)
}

// Manifests returns every loaded manifest, including problematic ones.
func (c *Chain) Manifests() []*backup.ClusterManifest {
	manifests := make([]*backup.ClusterManifest, 0)
	for _, group := range c.groups {
		for _, entry := range group.Entries {
			manifests = append(manifests, entry.Manifest)
		}
	}
	return manifests
}

// Problems returns a flat list of all own and inherited entry problems.
func (c *Chain) Problems() []*Problem {
	problems := make([]*Problem, 0)
	for _, group := range c.groups {
		for _, entry := range group.Entries {
			problems = append(problems, entry.Problems...)
		}
	}
	return problems
}

// ClusterPoints returns cluster recovery points ordered by effective timestamp.
func (c *Chain) ClusterPoints() []ClusterPoint {
	points := make([]ClusterPoint, len(c.points))
	copy(points, c.points)
	return points
}

// copyEntry shares the manifest pointer but clones Problems so callers cannot
// mutate the chain's state.
func copyEntry(entry *Entry) *Entry {
	return &Entry{
		Manifest: entry.Manifest,
		Problems: append([]*Problem(nil), entry.Problems...),
	}
}

// compareEntries orders entries lexicographically by backup_id.
func compareEntries(left, right *Entry) int {
	return cmp.Compare(left.Manifest.BackupID, right.Manifest.BackupID)
}

// topologyEqual reports whether two adjacent manifests have the same replicaset
// set and the same master on each.
func topologyEqual(a, b *backup.ClusterManifest) bool {
	if len(a.Topology.Replicasets) != len(b.Topology.Replicasets) {
		return false
	}

	for uuid := range a.Topology.Replicasets {
		if _, ok := b.Topology.Replicasets[uuid]; !ok {
			return false
		}

		// The master is the instance that was actually backed up.
		shardA := a.Shards[uuid]
		shardB := b.Shards[uuid]
		if shardA.Instance == nil || shardB.Instance == nil {
			continue
		}

		if shardA.Instance.InstanceUUID != shardB.Instance.InstanceUUID {
			return false
		}
	}
	return true
}

// replicasetUUIDs returns the sorted replicaset UUIDs of a manifest.
func replicasetUUIDs(m *backup.ClusterManifest) []string {
	uuids := make([]string, 0, len(m.Topology.Replicasets))
	for uuid := range m.Topology.Replicasets {
		uuids = append(uuids, uuid)
	}
	slices.Sort(uuids)
	return uuids
}
