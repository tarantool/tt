package cmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clustercli "github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/replicaset"
)

func TestDiscoverInstancesParallel(t *testing.T) {
	instanceNames := []string{"instance-1", "instance-2", "instance-3"}
	started := make(chan struct{}, len(instanceNames))
	release := make(chan struct{})
	done := make(chan struct{})

	var (
		topologies []replicaset.Replicasets
		hostnames  map[string]string
		reachable  map[string]bool
	)

	go func() {
		topologies, hostnames, reachable = discoverInstancesParallel(
			instanceNames,
			func(instName string) topologyDiscoveryResult {
				started <- struct{}{}

				<-release

				return topologyDiscoveryResult{
					topology: &replicaset.Replicasets{
						Replicasets: []replicaset.Replicaset{{Alias: instName}},
					},
					instanceUUID: instName,
					hostname:     instName + "-host",
					connected:    true,
				}
			},
		)

		close(done)
	}()

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()

	allStarted := true

	for range instanceNames {
		select {
		case <-started:
		case <-timer.C:
			allStarted = false
		}

		if !allStarted {
			break
		}
	}

	close(release)
	<-done

	require.True(t, allStarted)
	require.Len(t, topologies, len(instanceNames))

	for i, instName := range instanceNames {
		assert.Equal(t, instName, topologies[i].Replicasets[0].Alias)
		assert.Equal(t, instName+"-host", hostnames[instName])
		assert.True(t, reachable[instName])
	}
}

func TestLoadTopologyConfigFromFile(t *testing.T) {
	path := "../cluster/testdata/app/config.yaml"

	config, configDir, err := loadTopologyConfig(&cmdcontext.CmdCtx{}, path)
	require.NoError(t, err)
	assert.Equal(t, filepath.Dir(path), configDir)

	names, err := clustercli.Instances(config)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"b", "c"}, names)
}

func TestTopologyConfigInheritance(t *testing.T) {
	config, err := clustercli.BuildGoConfigFromBytes(t.Context(), []byte(`
iproto:
  listen:
    - uri: '127.0.0.1:3301'
groups:
  storage:
    replicasets:
      rs:
        database:
          replicaset_uuid: bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb
        instances:
          first:
            database:
              instance_uuid: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
            iproto:
              advertise:
                client: 'unix://./var/run/first.sock'
          second: {}
`))
	require.NoError(t, err)

	first, err := clustercli.InstanceConfig(config, "first")
	require.NoError(t, err)
	assert.Equal(t, "unix://./var/run/first.sock", topologyInstanceURI(first))

	second, err := clustercli.InstanceConfig(config, "second")
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:3301", topologyInstanceURI(second))

	topology := topologyFromConfig(config)
	require.Len(t, topology.Replicasets, 1)

	configured := topology.Replicasets[0]
	assert.Equal(t, "rs", configured.Alias)
	assert.Equal(t, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", configured.UUID)
	require.Len(t, configured.Instances, 2)
	assert.Equal(t, "first", configured.Instances[0].Alias)
	assert.Equal(t, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", configured.Instances[0].UUID)
	assert.Equal(t, "second", configured.Instances[1].Alias)
	assert.Empty(t, configured.Instances[1].UUID)
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
						Mode:  replicaset.ModeUnknown,
					},
					{
						UUID:  "inst-uuid-3",
						Alias: "instance-3",
						URI:   "host-3:3303",
						Mode:  replicaset.ModeRead,
					},
				},
			},
		},
	}
	hostnames := map[string]string{
		"inst-uuid-1": "node-1.example.com",
		"inst-uuid-2": "node-2.example.com",
	}
	reachable := map[string]bool{
		"inst-uuid-1": true,
		"inst-uuid-2": true,
	}

	topology := replicasetsToTopology(replicasets, hostnames, reachable)

	assert.Len(t, topology.Replicasets, 1)

	instances := topology.Replicasets["rs-uuid-1"]
	require.Len(t, instances, 3)

	assert.Equal(t, "inst-uuid-1", instances[0].InstanceUUID)
	assert.Equal(t, "instance-1", instances[0].InstanceName)
	assert.Equal(t, "node-1.example.com", instances[0].Hostname)
	assert.Equal(t, "rw", instances[0].Mode)
	assert.Equal(t, topologyStatusOK, instances[0].Status)

	assert.Equal(t, "inst-uuid-2", instances[1].InstanceUUID)
	assert.Equal(t, "instance-2", instances[1].InstanceName)
	assert.Equal(t, "node-2.example.com", instances[1].Hostname)
	assert.Equal(t, "unknown", instances[1].Mode)
	assert.Equal(t, topologyStatusOK, instances[1].Status)

	assert.Equal(t, "inst-uuid-3", instances[2].InstanceUUID)
	assert.Equal(t, "instance-3", instances[2].InstanceName)
	assert.Equal(t, "ro", instances[2].Mode)
	assert.Equal(t, topologyStatusNotReachable, instances[2].Status)
}
