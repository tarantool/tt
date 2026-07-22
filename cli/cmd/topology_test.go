package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
)

func TestLookupHostname(t *testing.T) {
	hostnames := map[string]string{
		"uuid-1": "node-1.example.com",
	}

	t.Run("found", func(t *testing.T) {
		assert.Equal(t, "node-1.example.com",
			lookupHostname("uuid-1", hostnames))
	})

	t.Run("not_found_fallback_to_uri", func(t *testing.T) {
		assert.Equal(t, "",
			lookupHostname("uuid-2", hostnames))
	})
}

func TestReplicasetsToTopology(t *testing.T) {
	replicasets := replicaset.Replicasets{
		Replicasets: []replicaset.Replicaset{
			{
				UUID:  "rs-uuid-1",
				Alias: "replicaset-1",
				Instances: []replicaset.Instance{
					{
						UUID:  "inst-uuid-1",
						Alias: "instance-1",
						URI:   "host-1:3301",
						Mode:  replicaset.ModeRW,
					},
					{
						UUID:  "inst-uuid-2",
						Alias: "instance-2",
						URI:   "host-2:3302",
						Mode:  replicaset.ModeRead,
					},
				},
			},
		},
	}
	hostnames := map[string]string{
		"inst-uuid-1": "node-1.example.com",
		// inst-uuid-2 is unreachable: should fall back to URI host.
	}

	topology := replicasetsToTopology(replicasets, hostnames)

	assert.Len(t, topology.Replicasets, 1)

	instances := topology.Replicasets["rs-uuid-1"]
	assert.Len(t, instances, 2)

	assert.Equal(t, "inst-uuid-1", instances[0].InstanceUUID)
	assert.Equal(t, "instance-1", instances[0].InstanceName)
	assert.Equal(t, "node-1.example.com", instances[0].Hostname)

	assert.Equal(t, "inst-uuid-2", instances[1].InstanceUUID)
	assert.Equal(t, "instance-2", instances[1].InstanceName)
	assert.Equal(t, "", instances[1].Hostname)
}

func TestStoreHostname(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		h := map[string]string{}
		storeHostname(h, []any{"uuid-1", "host-1"})
		assert.Equal(t, "host-1", h["uuid-1"])
	})

	t.Run("nil_hostname", func(t *testing.T) {
		h := map[string]string{}
		storeHostname(h, []any{"uuid-1", nil})
		assert.Equal(t, "", h["uuid-1"])
	})

	t.Run("too_short", func(t *testing.T) {
		h := map[string]string{}
		storeHostname(h, []any{"uuid-1"})
		assert.Empty(t, h)
	})

	t.Run("non_string_uuid", func(t *testing.T) {
		h := map[string]string{}
		storeHostname(h, []any{123, "host-1"})
		assert.Empty(t, h)
	})
}
