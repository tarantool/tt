package replicaset

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/running"
)

func TestExpelPeerConnected(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		upstream   string
		downstream string
		connected  bool
	}{
		{"none", "", "", false},
		{"follow both ways", "follow", "follow", true},
		{"upstream follow", "follow", "", true},
		{"upstream sync", "sync", "", true},
		{"upstream auth", "auth", "", true},
		{"upstream connected", "connected", "", true},
		{"downstream follow", "", "follow", true},
		{"stopped instance", "disconnected", "stopped", false},
		{"reconnecting", "connect", "stopped", false},
		{"refused while it shuts down", "loading", "stopped", false},
		{"replication error", "stopped", "", false},
		{"left the config of the peer", "", "stopped", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			peer := expelPeer{
				Name: "a", Replicaset: "r", UUID: "u",
				Upstream: test.upstream, Downstream: test.downstream,
				Inst: running.InstanceCtx{},
			}
			assert.Equal(t, test.connected, peer.connected())
		})
	}
}

func TestCheckExpelPeers(t *testing.T) {
	t.Parallel()

	stopped := []expelPeer{
		{Name: "a", Upstream: "disconnected", Downstream: "stopped"},
		{Name: "c", UUID: "uuid-b", Upstream: "connect"},
	}
	connected := []expelPeer{
		{Name: "a", UUID: "uuid-b", Upstream: "disconnected", Downstream: "stopped"},
		{Name: "c", UUID: "uuid-b", Upstream: "follow", Downstream: "follow"},
	}

	t.Run("disconnected", func(t *testing.T) {
		t.Parallel()

		// The UUID comes from the member that knows it.
		uuid, err := checkExpelPeers("app", "b", stopped, false)
		require.NoError(t, err)
		assert.Equal(t, "uuid-b", uuid)
	})

	t.Run("connected", func(t *testing.T) {
		t.Parallel()

		_, err := checkExpelPeers("app", "b", connected, false)
		require.ErrorIs(t, err, errExpelConnected)
		require.EqualError(t, err, `instance "b" is still connected to the replicaset: `+
			"stop it on its host (tt instance stop app:b) or pass --force "+
			"(c has upstream follow, downstream follow)")
	})

	t.Run("connected with force", func(t *testing.T) {
		t.Parallel()

		uuid, err := checkExpelPeers("app", "b", connected, true)
		require.NoError(t, err)
		assert.Equal(t, "uuid-b", uuid)
	})

	t.Run("no survivors", func(t *testing.T) {
		t.Parallel()

		_, err := checkExpelPeers("app", "b", nil, false)
		require.ErrorIs(t, err, errNoExpelPeer)
		require.ErrorContains(t, err, `pass --force to remove "b" from the config alone`)
	})

	t.Run("no survivors with force", func(t *testing.T) {
		t.Parallel()

		uuid, err := checkExpelPeers("app", "b", nil, true)
		require.NoError(t, err)
		assert.Empty(t, uuid)
	})

	t.Run("unknown to the survivors", func(t *testing.T) {
		t.Parallel()

		uuid, err := checkExpelPeers("app", "b", []expelPeer{{Name: "a"}}, false)
		require.NoError(t, err)
		assert.Empty(t, uuid)
	})
}

// expelConfig returns the config of a replicaset of a, b and c, where b is
// to be expelled, with leader, if any.
func expelConfig(t *testing.T, leader string) goconfig.Config {
	t.Helper()

	text := "groups:\n  g:\n    replicasets:\n      r:\n"
	if leader != "" {
		text += "        leader: " + leader + "\n"
	}

	text += "        instances:\n          a: {}\n          b: {}\n          c: {}\n" +
		"      other:\n        instances:\n          d: {}\n"

	mut, err := cluster.BuildMutableFromBytes(t.Context(), []byte(text))
	require.NoError(t, err)

	return mut.Snapshot()
}

func TestPlanExpel(t *testing.T) {
	t.Parallel()

	inst := cconfigInstance{
		failover: FailoverOff, groupName: "g", replicasetName: "r", name: "b",
	}

	// The instances of a project, none of them running: none is reached.
	dir := t.TempDir()
	candidates := []running.InstanceCtx{
		{AppName: "app", InstName: "a", ConsoleSocket: filepath.Join(dir, "a.control")},
		{AppName: "app", InstName: "b", ConsoleSocket: filepath.Join(dir, "b.control")},
		{AppName: "app", InstName: "d", ConsoleSocket: filepath.Join(dir, "d.control")},
	}

	t.Run("the leader", func(t *testing.T) {
		t.Parallel()

		_, err := planExpel(expelConfig(t, "b"), inst, candidates, true, true)
		require.ErrorIs(t, err, errExpelLeader)
	})

	t.Run("members not reached, strict", func(t *testing.T) {
		t.Parallel()

		_, err := planExpel(expelConfig(t, ""), inst, candidates, false, true)
		require.ErrorIs(t, err, errAllOtherInstancesInTheTargetReplicasetShouldBeOnline)
		require.ErrorContains(t, err, "could not connect to: a,c")
	})

	t.Run("no survivor", func(t *testing.T) {
		t.Parallel()

		_, err := planExpel(expelConfig(t, ""), inst, candidates, false, false)
		require.ErrorIs(t, err, errNoExpelPeer)
	})

	t.Run("no survivor with force", func(t *testing.T) {
		t.Parallel()

		plan, err := planExpel(expelConfig(t, ""), inst, candidates, true, true)
		require.NoError(t, err)
		assert.Empty(t, plan.survivors)
		assert.Empty(t, plan.uuid)
	})
}

// expelPeerPrelude replaces box.info and box.space with those of
// instance-001 of a replicaset, which sees instance-002 by its upstream and
// downstream, and has instance-003 in _cluster alone.
const expelPeerPrelude = `
local fake = {
    uuid = 'uuid-1',
    name = 'instance-001',
    replicaset = {uuid = 'rs-uuid', name = 'rs-001'},
    replication = {
        [1] = {id = 1, uuid = 'uuid-1', name = 'instance-001'},
        [2] = {id = 2, uuid = 'uuid-2', name = 'instance-002',
               upstream = {status = 'disconnected'},
               downstream = {status = 'stopped'}},
        [4] = {id = 4, uuid = 'uuid-4', name = 'instance-004',
               upstream = {status = 'follow'}, downstream = {status = 'follow'}},
    },
}
box.info = setmetatable(fake, {__call = function() return fake end})
local tuples = {
    {1, 'uuid-1', 'instance-001'},
    {3, 'uuid-3', 'instance-003'},
}
box.space = {_cluster = {pairs = function()
    return ipairs(tuples)
end}}
`

// runExpelPeerBody runs cconfigExpelPeerBody for name after
// expelPeerPrelude in tarantool and returns what it reports.
func runExpelPeerBody(t *testing.T, name string) map[string]string {
	t.Helper()

	binary, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found in PATH")
	}

	script := filepath.Join(t.TempDir(), "peer.lua")
	text := expelPeerPrelude +
		"local result = (function(...)\n" + cconfigExpelPeerBody + "\nend)('" + name + "')\n" +
		"io.stdout:write(require('json').encode(result))\nos.exit(0)\n"
	require.NoError(t, os.WriteFile(script, []byte(text), 0o600))

	out, err := exec.CommandContext(t.Context(), binary, script).Output()
	require.NoError(t, err, string(out))

	var peer map[string]string

	require.NoError(t, json.Unmarshal(out, &peer), string(out))

	return peer
}

func TestExpelPeerBody(t *testing.T) {
	t.Parallel()

	self := map[string]string{"name": "instance-001", "replicaset": "rs-001"}

	with := func(fields map[string]string) map[string]string {
		peer := maps.Clone(self)
		maps.Copy(peer, fields)

		return peer
	}

	assert.Equal(t, with(map[string]string{
		"uuid": "uuid-2", "upstream": "disconnected", "downstream": "stopped",
	}), runExpelPeerBody(t, "instance-002"))
	assert.Equal(t, with(map[string]string{
		"uuid": "uuid-4", "upstream": "follow", "downstream": "follow",
	}), runExpelPeerBody(t, "instance-004"))
	// Known by _cluster alone.
	assert.Equal(t, with(map[string]string{"uuid": "uuid-3"}), runExpelPeerBody(t, "instance-003"))
	assert.Equal(t, self, runExpelPeerBody(t, "instance-005"))
	// The member itself is not the instance to expel.
	assert.Equal(t, self, runExpelPeerBody(t, "instance-001"))
}
