package state_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// TestMaterializedRoundTrip reads back what WriteMaterialized recorded, each
// name once, and a later write replaces it whole.
func TestMaterializedRoundTrip(t *testing.T) {
	t.Parallel()

	lay := state.ProjectLayout(t.TempDir())

	require.NoError(t, state.WriteMaterialized(lay, []string{"metrics", "checks", "metrics"}))

	names, ok, err := state.ReadMaterialized(lay)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, map[string]bool{"checks": true, "metrics": true}, names)

	data, err := os.ReadFile(filepath.Join(lay.Manifests, state.MaterializedFile))
	require.NoError(t, err)
	assert.Contains(t, string(data), "checks\nmetrics\n", "one name a line, in name order")

	require.NoError(t, state.WriteMaterialized(lay, nil))

	names, ok, err = state.ReadMaterialized(lay)
	require.NoError(t, err)
	assert.True(t, ok, "an empty record is a record")
	assert.Empty(t, names)

	entries, err := os.ReadDir(lay.Manifests)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file is left behind")
}

// TestReadMaterializedOfATreeWithNone pins that a tree no tt recorded reads
// as no record, not as an empty one.
func TestReadMaterializedOfATreeWithNone(t *testing.T) {
	t.Parallel()

	names, ok, err := state.ReadMaterialized(state.ProjectLayout(t.TempDir()))
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, names)
}

// TestPackagesSkipTheMaterializedRecord pins that the record beside the
// metadata of the guests is no guest.
func TestPackagesSkipTheMaterializedRecord(t *testing.T) {
	t.Parallel()

	lay := state.ProjectLayout(t.TempDir())

	require.NoError(t, state.WriteMaterialized(lay, []string{"checks"}))
	require.NoError(t, state.WriteMetadata(lay, "monitoring",
		[]byte(manifestTOML("monitoring", nil)), nil, "1.0.0"))

	packages, err := state.Packages(lay)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	assert.Equal(t, "monitoring", packages[0].Name)
}
