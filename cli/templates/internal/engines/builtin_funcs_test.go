package engines

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenPort(t *testing.T) {
	state := newGenState()
	require.Equal(t, state.genPort(), 3301)
	require.Equal(t, state.genPort(), 3302)
}

func TestGenReplicasets(t *testing.T) {
	replicasets, err := genReplicasets("name", 4, 3)
	require.NoError(t, err)
	assert.Equal(t, replicasets, []replicaset{
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
	})

	replicasets, err = genReplicasets("name", 1, 27)
	require.NoError(t, err)
	assert.Equal(t, replicasets[0].InstNames[26], "name-001-027")

	_, err = genReplicasets("name", -1, 0)
	require.Error(t, err)
}
