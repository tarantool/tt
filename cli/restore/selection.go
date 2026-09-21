package restore

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
)

// selection is the set of replicasets a restore is restricted to, named the way
// the cluster configuration names them. A nil *selection restores everything
// the backup holds, which is what an absent --replicasets means.
type selection struct {
	// names holds the selected replicaset names.
	names map[string]bool
}

// resolveSelection turns the names --replicasets was given into a selection
// against the cluster configuration. An empty list is no selection at all.
//
// Every name is checked here, before the storage is read: a mistyped
// replicaset would otherwise surface after a whole chain has been walked, and
// the operator who typed it would see a recovery point that quietly covers
// fewer replicasets than they asked for.
func resolveSelection(current *ClusterTopology, names []string) (*selection, error) {
	if len(names) == 0 {
		return nil, nil
	}

	if current == nil {
		return nil, errors.New(
			"--replicasets requires -c: the names come from the cluster config")
	}

	configured := make([]string, 0, len(current.Replicasets))
	for _, replicaset := range current.Replicasets {
		configured = append(configured, replicaset.Name)
	}

	slices.Sort(configured)

	selected := make(map[string]bool, len(names))

	for _, name := range names {
		if name == "" {
			return nil, fmt.Errorf(
				"--replicasets: empty replicaset name (configured: %s)",
				strings.Join(configured, ", "))
		}

		if !slices.Contains(configured, name) {
			return nil, fmt.Errorf(
				"--replicasets: replicaset %q is not in the cluster config "+
					"(configured: %s)", name, strings.Join(configured, ", "))
		}

		selected[name] = true
	}

	return &selection{names: selected}, nil
}

// CheckReplicasets reports whether the names --replicasets was given can be
// resolved against the cluster configuration. Plan resolves them itself; a
// caller that has to open a backup storage before it can plan runs this first,
// so that a name it can refuse on its own costs no storage at all.
func CheckReplicasets(current *ClusterTopology, names []string) error {
	_, err := resolveSelection(current, names)

	return err //nolint:wrapcheck
}

// configured narrows the cluster configuration to the selected replicasets, so
// that the topology comparison weighs the replicasets a restore is aimed at and
// nothing else. A nil selection is the whole configuration.
func (s *selection) configured(current ClusterTopology) ClusterTopology {
	if s == nil {
		return current
	}

	replicasets := make([]ConfiguredReplicaset, 0, len(current.Replicasets))
	for _, replicaset := range current.Replicasets {
		if s.names[replicaset.Name] {
			replicasets = append(replicasets, replicaset)
		}
	}

	return ClusterTopology{Replicasets: replicasets}
}

// unselected returns the configured replicasets the selection leaves out, by
// name and in name order. They are the ones an operator has to bootstrap
// themselves, so they are reported rather than passed over.
func (s *selection) unselected(current ClusterTopology) []string {
	if s == nil {
		return nil
	}

	var names []string

	for _, replicaset := range current.Replicasets {
		if !s.names[replicaset.Name] {
			names = append(names, replicaset.Name)
		}
	}

	slices.Sort(names)

	return names
}

// instances returns the instance names of the selected replicasets, sorted.
// That is the shape the chain takes a selection in: a manifest knows instances
// and replicaset UUIDs, never the replicaset names the configuration gives.
func (s *selection) instances(current ClusterTopology) []string {
	if s == nil {
		return nil
	}

	var names []string

	for _, replicaset := range current.Replicasets {
		if s.names[replicaset.Name] {
			names = append(names, replicaset.Instances...)
		}
	}

	slices.Sort(names)

	return names
}

// chainSelection renders the selection the way chain.Load takes it. A nil
// selection stays nil there too, and the chain then stitches points over every
// replicaset it finds.
func (s *selection) chainSelection(current *ClusterTopology) *chain.Selection {
	if s == nil || current == nil {
		return nil
	}

	return chain.SelectInstances(s.instances(*current))
}

// unselectedWarnings describes, one line each, the configured replicasets the
// selection leaves out.
func (s *selection) unselectedWarnings(current ClusterTopology) []string {
	names := s.unselected(current)

	warnings := make([]string, 0, len(names))
	for _, name := range names {
		warnings = append(warnings, fmt.Sprintf(
			"replicaset %q is configured but not selected: it is not restored, "+
				"bootstrap it fresh", name))
	}

	return warnings
}

// pointTopology is the topology of the replicasets a point is restored over: a
// point carries the whole topology of the segment it was stitched in, and only
// the replicasets it holds positions for are being restored. Without a
// selection every replicaset of the segment holds one, so this is the segment
// topology itself.
func pointTopology(point chain.ClusterPoint) backup.Topology {
	replicasets := make(map[string][]backup.TopologyInstance, len(point.Shards))

	for replicasetUUID := range point.Shards {
		if instances, ok := point.Topology.Replicasets[replicasetUUID]; ok {
			replicasets[replicasetUUID] = instances
		}
	}

	return backup.Topology{Replicasets: replicasets}
}
