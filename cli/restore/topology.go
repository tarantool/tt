package restore

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup"
)

// ClusterTopology is the cluster composition a recovery point is checked
// against.
//
// It is read from the cluster configuration, not from the running instances.
// The scenario this check exists for is a cluster whose nodes are dead or in a
// crash loop, and a source that had to connect to them would fail long before
// the check ever ran. Instance UUIDs are deliberately not part of it: the
// sanctioned restore target is a freshly deployed cluster, where every UUID is
// new and the config may carry none at all, and `tt restore apply` stamps one
// into the node each replicaset is restored onto afterwards.
type ClusterTopology struct {
	// Replicasets are the replicasets the configuration declares.
	Replicasets []ConfiguredReplicaset
}

// ConfiguredReplicaset is one replicaset of the current configuration.
type ConfiguredReplicaset struct {
	// Name is the replicaset name; the configuration has no other stable
	// identifier for it once the UUIDs have been re-generated.
	Name string
	// Instances are the instance names configured in the replicaset.
	Instances []string
}

// TopologyDiff explains why the topology a recovery point was taken on cannot
// be laid onto the cluster the configuration describes. Only replicaset-level
// differences land here; a replaced replica inside a matched replicaset is a
// normal companion of a restore and is reported as a warning instead.
type TopologyDiff struct {
	// MissingReplicasets were backed up but no configured replicaset shares an
	// instance name with them: their data has nowhere to go.
	MissingReplicasets []ReplicasetDiff `json:"missing_replicasets,omitempty"`
	// ExtraReplicasets are configured but nothing in the backup maps onto them.
	ExtraReplicasets []ReplicasetDiff `json:"extra_replicasets,omitempty"`
	// AmbiguousReplicasets share instance names with more than one replicaset
	// on the other side, so no mapping can be picked without guessing.
	AmbiguousReplicasets []ReplicasetDiff `json:"ambiguous_replicasets,omitempty"`
	// UncoveredMasters are backed-up masters the matched replicaset of the
	// configuration does not carry.
	UncoveredMasters []MasterDiff `json:"uncovered_masters,omitempty"`
}

// ReplicasetDiff names one replicaset by whatever identifies it on its side:
// the backup knows a replicaset UUID, the configuration a name, and both know
// the instance names the matching is done on.
type ReplicasetDiff struct {
	ReplicasetUUID string `json:"replicaset_uuid,omitempty"`
	Name           string `json:"name,omitempty"`
	// Instances are the instance names of this replicaset.
	Instances []string `json:"instances"`
	// Candidates are the replicasets on the other side it could equally be.
	Candidates []string `json:"candidates,omitempty"`
}

// MasterDiff names a backed-up master that the configuration has no instance
// for. The archive of a replicaset is the master's, so a restore that cannot
// place it drops that replicaset's data without saying so.
type MasterDiff struct {
	ReplicasetUUID string `json:"replicaset_uuid"`
	Name           string `json:"name"`
	MasterInstance string `json:"master_instance_name"`
}

// Empty reports whether the topologies can be laid onto one another.
func (d TopologyDiff) Empty() bool {
	return !d.replicasetLevel() && len(d.UncoveredMasters) == 0
}

// replicasetLevel reports whether the sets of replicasets themselves differ, as
// opposed to a matched replicaset whose master the config no longer carries.
func (d TopologyDiff) replicasetLevel() bool {
	return len(d.MissingReplicasets) > 0 ||
		len(d.ExtraReplicasets) > 0 ||
		len(d.AmbiguousReplicasets) > 0
}

// topologyMatch is the outcome of laying a point's topology onto the current
// configuration.
type topologyMatch struct {
	// matched maps a backed-up replicaset UUID onto the configured replicaset
	// holding its instances.
	matched map[string]ConfiguredReplicaset
	// diff is empty exactly when every replicaset found one home.
	diff TopologyDiff
	// warnings are the differences a restore survives.
	warnings []string
}

// matchTopology maps every backed-up replicaset onto a configured one by the
// instance names they share. Names, never UUIDs: a restore into a redeployed
// cluster has new UUIDs everywhere, and comparing them would reject exactly the
// case the check exists for.
func matchTopology(backedUp backup.Topology, current ClusterTopology) topologyMatch {
	configured := make(map[string][]string, len(current.Replicasets))
	for _, replicaset := range current.Replicasets {
		instances := slices.Clone(replicaset.Instances)
		slices.Sort(instances)
		configured[replicaset.Name] = instances
	}

	match := topologyMatch{matched: make(map[string]ConfiguredReplicaset)}
	// Two backed-up replicasets landing on one configured replicaset is as
	// unmappable as none landing on it, and it is only visible once every
	// replicaset has been placed.
	claims := make(map[string][]string, len(configured))

	for _, replicasetUUID := range slices.Sorted(maps.Keys(backedUp.Replicasets)) {
		instances := instanceNames(backedUp.Replicasets[replicasetUUID])
		candidates := intersecting(configured, instances)

		switch len(candidates) {
		case 0:
			match.diff.MissingReplicasets = append(match.diff.MissingReplicasets,
				ReplicasetDiff{ReplicasetUUID: replicasetUUID, Instances: instances})
		case 1:
			claims[candidates[0]] = append(claims[candidates[0]], replicasetUUID)
			match.matched[replicasetUUID] = ConfiguredReplicaset{
				Name:      candidates[0],
				Instances: configured[candidates[0]],
			}
		default:
			match.diff.AmbiguousReplicasets = append(match.diff.AmbiguousReplicasets,
				ReplicasetDiff{
					ReplicasetUUID: replicasetUUID,
					Instances:      instances,
					Candidates:     candidates,
				})
		}
	}

	for _, name := range slices.Sorted(maps.Keys(configured)) {
		owners := claims[name]
		switch {
		case len(owners) == 0:
			match.diff.ExtraReplicasets = append(match.diff.ExtraReplicasets,
				ReplicasetDiff{Name: name, Instances: configured[name]})
		case len(owners) > 1:
			match.diff.AmbiguousReplicasets = append(match.diff.AmbiguousReplicasets,
				ReplicasetDiff{
					Name:       name,
					Instances:  configured[name],
					Candidates: owners,
				})

			for _, replicasetUUID := range owners {
				delete(match.matched, replicasetUUID)
			}
		}
	}

	match.warnings = instanceWarnings(backedUp, match.matched)

	return match
}

// instanceWarnings describes the instance-level differences inside matched
// replicasets. None of them blocks: an instance the configuration no longer has
// is a replica that was replaced, which is what a restore usually follows.
//
// The other direction is deliberately not a warning. A configured instance the
// backup never saw is in no way exceptional: only the restore target replays
// the archives, and every other member of the replicaset is wiped and rejoins
// by name whether or not the backup knew it. Those members are named in
// restore_targets.rejoin, as a step of the procedure rather than a difference
// to worry about - a manifest whose topology holds masters alone would
// otherwise report every replica of every replicaset on every successful plan.
func instanceWarnings(
	backedUp backup.Topology,
	matched map[string]ConfiguredReplicaset,
) []string {
	warnings := make([]string, 0)

	for _, replicasetUUID := range slices.Sorted(maps.Keys(matched)) {
		replicaset := matched[replicasetUUID]
		instances := instanceNames(backedUp.Replicasets[replicasetUUID])

		if gone := missing(instances, replicaset.Instances); len(gone) > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"replicaset %s (%s): %s backed up but not in the cluster config",
				replicasetUUID, replicaset.Name, strings.Join(gone, ", ")))
		}
	}

	return warnings
}

// uncoveredMasters returns the backed-up masters whose instance name the
// matched replicaset does not carry. The archive of a replicaset holds the
// master's files, so an operator has no node to hand it to.
func uncoveredMasters(
	masters map[string]string,
	matched map[string]ConfiguredReplicaset,
) []MasterDiff {
	var uncovered []MasterDiff

	for _, replicasetUUID := range slices.Sorted(maps.Keys(masters)) {
		master := masters[replicasetUUID]
		replicaset, ok := matched[replicasetUUID]
		if !ok || master == "" {
			continue
		}

		if !slices.Contains(replicaset.Instances, master) {
			uncovered = append(uncovered, MasterDiff{
				ReplicasetUUID: replicasetUUID,
				Name:           replicaset.Name,
				MasterInstance: master,
			})
		}
	}

	return uncovered
}

// intersecting returns the names of the configured replicasets sharing at least
// one instance name with the given ones, ordered by name.
func intersecting(configured map[string][]string, instances []string) []string {
	var candidates []string

	for _, name := range slices.Sorted(maps.Keys(configured)) {
		for _, instance := range configured[name] {
			if slices.Contains(instances, instance) {
				candidates = append(candidates, name)
				break
			}
		}
	}

	return candidates
}

// instanceNames returns the sorted instance names of a backed-up replicaset.
func instanceNames(instances []backup.TopologyInstance) []string {
	names := make([]string, 0, len(instances))
	for _, instance := range instances {
		if instance.InstanceName != "" {
			names = append(names, instance.InstanceName)
		}
	}

	slices.Sort(names)

	return names
}

// missing returns the elements of left that right does not contain.
func missing(left, right []string) []string {
	var result []string
	for _, item := range left {
		if !slices.Contains(right, item) {
			result = append(result, item)
		}
	}

	return result
}
