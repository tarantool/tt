//go:build integration

package cmd

import (
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	etcdtest "github.com/tarantool/go-storage/v2/test_helpers/etcd"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// etcdReference returns a cluster config file that keeps the cluster config
// in the etcd at endpoint, under prefix.
func etcdReference(endpoint, prefix string) string {
	return fmt.Sprintf("config:\n  etcd:\n    endpoints:\n      - %s\n    prefix: %s\n"+
		"    http:\n      request:\n        timeout: 0.5\n", endpoint, prefix)
}

// storedTopology returns the instances of the cluster config the file at
// path reads with the storage it points to.
func storedTopology(t *testing.T, path string) []string {
	t.Helper()

	cfg, err := cluster.GetClusterConfig(t.Context(), path, integrity.IntegrityCtx{})
	require.NoError(t, err)

	topology, err := cluster.Instances(cfg.Snapshot())
	require.NoError(t, err)

	return topology
}

// TestClusterPublishIntoEmptyStorage pins that the first publication into the
// pair of an application whose config.yaml points to an empty etcd goes
// there: the pair is read without the config the storage does not hold yet.
// The config of an instance has no cluster config to go into before it, and
// is refused saying so; after it, the config is checked against what "*"
// stands for, and the config of an instance goes into it.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishIntoEmptyStorage(t *testing.T) {
	inst := etcdtest.NewLazyCluster(etcdtest.ClusterConfig{Size: 1})
	defer inst.Terminate()

	reference := etcdReference(inst.EndpointsGRPC()[0], "/p")
	config := publishProject(t, "\"*\":\n", reference)
	cmdCtx := &cmdcontext.CmdCtx{}

	instance := publishSource(t, "iproto:\n  listen:\n    - uri: 127.0.0.1:3399\n")

	err := internalClusterPublishModule(cmdCtx, []string{"myapp:s-1", instance})
	require.ErrorContains(t, err, `a configuration data not found in etcd for prefix "/p"; `+
		"there is no cluster config to set the config of the instance in: publish the "+
		"cluster config first")

	source := publishSource(t, topologyOf("s-1", "s-2"))
	require.NoError(t, internalClusterPublishModule(cmdCtx, []string{"myapp", source}))

	data, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, reference, string(data), "the file keeps its reference")
	assert.Equal(t, []string{"s-1", "s-2"}, storedTopology(t, config))

	source = publishSource(t, topologyOf("s-1"))
	err = internalClusterPublishModule(cmdCtx, []string{"myapp", source})
	require.ErrorIs(t, err, app.ErrTopologyMismatch, `"*" stands for s-2 now`)

	require.NoError(t, internalClusterPublishModule(cmdCtx, []string{"myapp:s-1", instance}))
	assert.Equal(t, []string{"s-1", "s-2"}, storedTopology(t, config))

	cfg, err := cluster.GetClusterConfig(t.Context(), config, integrity.IntegrityCtx{})
	require.NoError(t, err)

	snapshot := cfg.Snapshot()

	merged, err := snapshot.MarshalYAML()
	require.NoError(t, err)
	assert.Contains(t, string(merged), "127.0.0.1:3399")
}

// TestClusterPublishIntoStorageOutOfReach pins that a storage the pair
// points to that is out of reach fails the publication itself, saying so,
// and leaves the file as it is.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishIntoStorageOutOfReach(t *testing.T) {
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	reference := etcdReference("http://"+address, "/p")
	config := publishProject(t, "\"*\":\n", reference)

	source := publishSource(t, topologyOf("s-1"))

	err = internalClusterPublishModule(&cmdcontext.CmdCtx{}, []string{"myapp", source})
	require.ErrorContains(t, err, "unable to connect to the config.etcd of "+config+
		": failed to create etcd client")

	data, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, reference, string(data))
}

// TestClusterPublishMendsBrokenFile pins that a config.yaml that cannot be
// read as a cluster config, which every other command refuses, is mended by
// publishing into it.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterPublishMendsBrokenFile(t *testing.T) {
	config := publishProject(t, "s-1:\n", "groups: [\n")

	source := publishSource(t, topologyOf("s-1"))
	require.NoError(t, internalClusterPublishModule(&cmdcontext.CmdCtx{},
		[]string{"myapp", source}))

	data, err := os.ReadFile(config)
	require.NoError(t, err)
	assert.Equal(t, topologyOf("s-1"), string(data))
}
