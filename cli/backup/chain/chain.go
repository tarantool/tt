package chain

import (
	"cmp"
	"slices"
	"time"

	"github.com/tarantool/tt/cli/backup"
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
	// coverageStart is the creation time of the earliest manifest, zero when no
	// manifest was loaded. It separates "this storage holds nothing for that
	// time" from "it does, but no point sits there", which resolve to different
	// statuses and different exit codes.
	coverageStart time.Time
}

// covers reports whether t falls at or after the earliest stored manifest.
func (c *Chain) covers(t time.Time) bool {
	return !c.coverageStart.IsZero() && !t.Before(c.coverageStart)
}

// orderedEntries returns every entry from the oldest full backup to the newest,
// following previous_backup_id within each group. It is the order to walk when
// an answer must not depend on map iteration.
func (c *Chain) orderedEntries() []*Entry {
	var entries []*Entry
	for _, group := range c.groups {
		entries = append(entries, group.Entries...)
	}

	return entries
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

// topologyEqual reports whether two adjacent manifests have the same
// replicaset set and the same membership on each. A member is identified by
// its UUID, and the composition is that set: a member added, removed, or
// replaced under the same name between two backups means the replicaset had
// no single composition across the window, which is what keeps the points on
// either side from stitching into one. The name a member carries counts only
// when both manifests bound one to the same UUID and the two disagree: a
// member gaining or dropping its name is the same member, while a rename
// keeping the UUID -- which Tarantool refuses today -- would be a boundary.
// A master promotion alone is deliberately not a boundary: it changes none
// of the members, and which of them took the backup follows from the
// manifest of the point the chain resolves to.
func topologyEqual(a, b *backup.ClusterManifest) bool {
	if len(a.Topology.Replicasets) != len(b.Topology.Replicasets) {
		return false
	}

	for uuid := range a.Topology.Replicasets {
		if _, ok := b.Topology.Replicasets[uuid]; !ok {
			return false
		}

		if !membershipEqual(a.Topology.Replicasets[uuid], b.Topology.Replicasets[uuid]) {
			return false
		}
	}

	return true
}

// membershipEqual reports whether two views of one replicaset's membership
// are one composition: the same UUID set, and no UUID that both sides named
// with two different names. The set is what the restored _cluster has to
// carry, so a member gaining or dropping its name is no composition change;
// a rename with the UUID kept would refuse the stitch, and Tarantool
// refusing a rename today is the only reason that case stays hypothetical.
func membershipEqual(a, b []backup.TopologyInstance) bool {
	aMembers := memberIdentities(a)
	bMembers := memberIdentities(b)

	if len(aMembers) != len(bMembers) {
		return false
	}

	for uuid, aName := range aMembers {
		bName, ok := bMembers[uuid]
		if !ok {
			return false
		}

		if aName != "" && bName != "" && aName != bName {
			return false
		}
	}

	return true
}

// memberIdentities returns the membership of a replicaset keyed by UUID:
// the UUID -> the instance name the manifest bound to it, empty for a member
// the manifest recorded without one.
func memberIdentities(instances []backup.TopologyInstance) map[string]string {
	members := make(map[string]string, len(instances))
	for _, instance := range instances {
		members[instance.InstanceUUID] = instance.InstanceName
	}

	return members
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
