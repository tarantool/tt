package replicaset

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// topologyBodyPrelude replaces box.info, box.cfg and the config module
// with those of an instance of a replicaset whose member of replica id 2
// was expelled: box.info.replication has the ids 1, 3 and 4, with a hole.
const topologyBodyPrelude = `
local fake = {
    uuid = 'uuid-1',
    ro = false,
    replicaset = {uuid = 'rs-uuid', name = 'rs-001'},
    election = {leader = 0},
    replication = {
        [1] = {id = 1, uuid = 'uuid-1', name = 'instance-001'},
        [3] = {id = 3, uuid = 'uuid-3', name = 'instance-003',
               upstream = {peer = 'replicator@unix/:./instance-003.iproto'}},
        [4] = {id = 4, uuid = 'uuid-4', name = 'instance-004',
               upstream = {peer = 'replicator@unix/:./instance-004.iproto'}},
    },
}
box.info = setmetatable(fake, {__call = function() return fake end})
box.cfg = {listen = 'unix/:./instance-001.iproto', read_only = false}
package.loaded.config = {get = function() return {replication = {failover = 'off'}} end}
`

// runTopologyBody runs body after topologyBodyPrelude in tarantool and
// returns the aliases and URIs of the instances it reports.
func runTopologyBody(t *testing.T, body string) ([]string, []string) {
	t.Helper()

	binary, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found in PATH")
	}

	script := filepath.Join(t.TempDir(), "topology.lua")
	text := topologyBodyPrelude +
		"local result = (function(...)\n" + body + "\nend)()\n" +
		"io.stdout:write(require('json').encode(result))\nos.exit(0)\n"
	require.NoError(t, os.WriteFile(script, []byte(text), 0o600))

	out, err := exec.CommandContext(t.Context(), binary, script).Output()
	require.NoError(t, err, string(out))

	var topology struct {
		Instances []struct {
			Alias string `json:"alias"`
			URI   string `json:"uri"`
		} `json:"instances"`
	}

	require.NoError(t, json.Unmarshal(out, &topology), string(out))

	aliases := make([]string, 0, len(topology.Instances))
	uris := make([]string, 0, len(topology.Instances))

	for _, instance := range topology.Instances {
		aliases = append(aliases, instance.Alias)
		uris = append(uris, instance.URI)
	}

	return aliases, uris
}

// TestTopologyBodiesSeeEveryReplica checks that the topology an instance
// reports, of the centralized config and of a custom application, has
// every member of box.info.replication, also those past the hole an
// expelled member leaves in its replica ids, in the order of the ids: with
// ipairs the instances after the hole were left out, and tt replicaset
// status showed a member running and replicating as gone.
func TestTopologyBodiesSeeEveryReplica(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"cconfig": cconfigGetInstanceTopologyBody,
		"custom":  customGetInstanceTopologyBody,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			aliases, uris := runTopologyBody(t, body)

			assert.Equal(t, []string{"instance-001", "instance-003", "instance-004"}, aliases)
			assert.Equal(t, []string{
				"unix/:./instance-001.iproto",
				"unix/:./instance-003.iproto",
				"unix/:./instance-004.iproto",
			}, uris)
		})
	}
}
