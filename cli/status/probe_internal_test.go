package status

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/internal/instances"
)

// TestStartStateIsUp pins when a starting Tarantool instance counts as up, by
// what it tells of its box and its config: an instance of the cluster config
// needs both, one that runs its script the box alone.
func TestStartStateIsUp(t *testing.T) {
	cases := []struct {
		state   startState
		cluster bool
		script  bool
	}{
		{startState{Box: "running", Config: "ready"}, true, true},
		{startState{Box: "orphan", Config: "check_warnings"}, true, true},
		{startState{Box: "running", Config: "check_errors"}, true, true},
		{startState{Box: "loading", Config: "startup_in_progress"}, false, false},
		{startState{Box: "running", Config: "startup_in_progress"}, false, true},
		{startState{Box: "running", Config: "uninitialized"}, false, true},
		{startState{Box: "", Config: ""}, false, false},
	}

	for _, test := range cases {
		up, described := test.state.isUp(false)
		assert.Equal(t, test.cluster, up, "%+v", test.state)
		assert.Contains(t, described, "CONFIG ")

		up, described = test.state.isUp(true)
		assert.Equal(t, test.script, up, "%+v", test.state)
		assert.NotContains(t, described, "CONFIG", "a script instance has no cluster config")
	}

	_, described := startState{Box: "", Config: ""}.isUp(false)
	assert.Equal(t, "CONFIG --, BOX --", described)
}

// TestProbeNotAnswering pins that an instance with no console socket to
// answer on is no instance that is up, but one that does not answer yet.
func TestProbeNotAnswering(t *testing.T) {
	inst := running.InstanceCtx{
		Kind:          instances.KindCluster,
		ConsoleSocket: filepath.Join(t.TempDir(), "tarantool.control"),
	}

	up, _, err := Probe(t.Context(), &inst)
	require.Error(t, err)
	assert.False(t, up)
}
