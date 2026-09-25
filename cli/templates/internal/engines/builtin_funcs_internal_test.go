package engines

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenPort(t *testing.T) {
	state := newGenState()
	require.Equal(t, 3301, state.genPort())
	require.Equal(t, 3302, state.genPort())
}

func TestGenMetricsPort(t *testing.T) {
	state := newGenState()
	require.Equal(t, 8081, state.genMetricsPort())
	require.Equal(t, 8082, state.genMetricsPort())
}

func TestGenReplicasets(t *testing.T) {
	replicasets, err := genReplicasets("name", 4, 3)
	require.NoError(t, err)
	assert.Equal(t, []replicaset{
		{
			Name:      "name-001",
			InstNames: []string{"name-001-a", "name-001-b", "name-001-c"},
		},
		{
			Name:      "name-002",
			InstNames: []string{"name-002-a", "name-002-b", "name-002-c"},
		},
		{
			Name:      "name-003",
			InstNames: []string{"name-003-a", "name-003-b", "name-003-c"},
		},
		{
			Name:      "name-004",
			InstNames: []string{"name-004-a", "name-004-b", "name-004-c"},
		},
	}, replicasets)

	replicasets, err = genReplicasets("name", 1, 27)
	require.NoError(t, err)
	assert.Equal(t, "name-001-027", replicasets[0].InstNames[26])

	_, err = genReplicasets("name", -1, 0)
	require.Error(t, err)
}
