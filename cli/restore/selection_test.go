package restore

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
)

// TestResolveSelectionWithoutNamesSelectsEverything covers a run with no
// --replicasets: there is no selection, and every method of one behaves as the
// identity, so the plan is what it is without the flag.
func TestResolveSelectionWithoutNamesSelectsEverything(t *testing.T) {
	current := *shardedCluster()

	selected, err := resolveSelection(&current, nil)

	require.NoError(t, err)
	require.Nil(t, selected)

	require.Equal(t, current, selected.configured(current))
	require.Empty(t, selected.unselected(current))
	require.Empty(t, selected.instances(current))
	require.Empty(t, selected.unselectedWarnings(current))
	require.Nil(t, selected.chainSelection(&current))
}

// TestResolveSelectionNeedsAClusterConfig covers --replicasets without -c. The
// names live in the configuration alone: a manifest knows instance names and
// replicaset UUIDs, and nothing could resolve the flag against it.
func TestResolveSelectionNeedsAClusterConfig(t *testing.T) {
	selected, err := resolveSelection(nil, []string{"storage-a"})

	require.Nil(t, selected)
	require.ErrorContains(t, err, "--replicasets requires -c")
	require.ErrorContains(t, err, "the names come from the cluster config")
}

// TestResolveSelectionRejectsAnUnknownName covers a mistyped replicaset. The
// error names what was asked for and what the configuration declares, so that
// the operator can see which of the two is wrong without reading the config.
func TestResolveSelectionRejectsAnUnknownName(t *testing.T) {
	current := *shardedCluster()

	selected, err := resolveSelection(&current, []string{"storage-a", "storage-z"})

	require.Nil(t, selected)
	require.ErrorContains(t, err, `--replicasets: replicaset "storage-z" is not `+
		`in the cluster config (configured: router-001, storage-a, storage-b)`)
}

// TestResolveSelectionRejectsAnEmptyName covers --replicasets=a,,b and
// --replicasets="": an empty entry names no replicaset, and taking it for one
// would silently restore fewer than were asked for.
func TestResolveSelectionRejectsAnEmptyName(t *testing.T) {
	current := *shardedCluster()

	selected, err := resolveSelection(&current, []string{"storage-a", ""})

	require.Nil(t, selected)
	require.ErrorContains(t, err, "--replicasets: empty replicaset name")
}

// TestResolveSelectionCollapsesDuplicates covers a name given twice: it is one
// replicaset either way, and the instances it expands into must not be doubled.
func TestResolveSelectionCollapsesDuplicates(t *testing.T) {
	current := *shardedCluster()

	selected, err := resolveSelection(&current,
		[]string{"storage-a", "storage-a", "storage-b"})

	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, []string{masterOfA, masterOfB}, selected.instances(current))
}

// TestSelectionNarrowsBothSidesOfTheComparison covers what the topology check
// is handed: the configuration without the replicasets that were left out, and
// their names to report instead.
func TestSelectionNarrowsBothSidesOfTheComparison(t *testing.T) {
	current := *shardedCluster(configuredReplicaset("storage-c", masterOfC))

	selected, err := resolveSelection(&current, []string{"storage-b", "storage-a"})
	require.NoError(t, err)

	require.Equal(t, ClusterTopology{Replicasets: []ConfiguredReplicaset{
		configuredReplicaset("storage-a", masterOfA),
		configuredReplicaset("storage-b", masterOfB),
	}}, selected.configured(current))

	require.Equal(t, []string{"router-001", "storage-c"}, selected.unselected(current))
	require.Equal(t, []string{
		routerWarning,
		`replicaset "storage-c" is configured but not selected: ` +
			`it is not restored, bootstrap it fresh`,
	}, selected.unselectedWarnings(current))

	require.Equal(t, []string{masterOfA, masterOfB}, selected.instances(current))
	require.NotNil(t, selected.chainSelection(&current))
}

// TestPointTopologyKeepsOnlyTheRestoredReplicasets covers the other side of the
// comparison. A point carries the whole topology of its segment, including the
// replicasets a selection left out of its positions, and only the ones it holds
// a position for are being restored.
func TestPointTopologyKeepsOnlyTheRestoredReplicasets(t *testing.T) {
	segment := backup.Topology{Replicasets: map[string][]backup.TopologyInstance{
		shardA: {{InstanceName: masterOfA}},
		shardB: {{InstanceName: masterOfB}},
		shardR: {{InstanceName: routerOfR}},
	}}

	point := chain.ClusterPoint{
		Topology: segment,
		Shards: map[string]chain.Position{
			shardA: {ReplicaID: replicaOfA, LSN: 500},
			shardB: {ReplicaID: replicaOfB, LSN: 400},
		},
	}

	require.Equal(t, backup.Topology{Replicasets: map[string][]backup.TopologyInstance{
		shardA: {{InstanceName: masterOfA}},
		shardB: {{InstanceName: masterOfB}},
	}}, pointTopology(point))

	// A point stitched over every replicaset of its segment - what a run
	// without the flag produces - keeps the segment topology whole.
	whole := chain.ClusterPoint{
		Topology: segment,
		Shards: map[string]chain.Position{
			shardA: {ReplicaID: replicaOfA, LSN: 500},
			shardB: {ReplicaID: replicaOfB, LSN: 400},
			shardR: {ReplicaID: replicaOfR, LSN: 10},
		},
	}

	require.Equal(t, segment, pointTopology(whole))
}
