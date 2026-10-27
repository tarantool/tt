//go:build integration

package cluster_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cluster"
	clustercmd "github.com/tarantool/tt/v3/cli/cluster/cmd"
)

// storageRefConfig returns a cluster config file that points to the etcd at
// endpoint, under the prefix /test, and declares the instance file-1 itself.
func storageRefConfig(endpoint string) string {
	return fmt.Sprintf("config:\n  etcd:\n    endpoints:\n      - %s\n"+
		"    username: root\n    password: pass\n    prefix: /test\n"+
		"groups:\n  g:\n    replicasets:\n      r:\n        instances:\n          file-1: {}\n",
		endpoint)
}

// etcdClient connects to the etcd of inst as root.
func etcdClient(t *testing.T, endpoints []string) *clientv3.Client {
	t.Helper()

	client, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
		Username:  "root",
		Password:  "pass",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return client
}

// etcdGet returns the value of key in etcd.
func etcdGet(t *testing.T, client *clientv3.Client, key string) string {
	t.Helper()

	var value string

	err := doWithCtx(func(ctx context.Context) error {
		resp, err := client.Get(ctx, key)
		if err != nil {
			return err
		}

		if len(resp.Kvs) > 0 {
			value = string(resp.Kvs[0].Value)
		}

		return nil
	})
	require.NoError(t, err)

	return value
}

// TestPublishAppToStorage pins that a config published into the pair of an
// application whose config.yaml points to etcd goes into etcd, under the
// prefix and with the credentials of the reference, and that the file keeps
// its reference; the topology it is checked with includes the instances the
// file declares itself.
func TestPublishAppToStorage(t *testing.T) {
	inst := startEtcd(t, etcdOpts{Username: "root", Password: "pass"})
	defer inst.Terminate()

	endpoints := inst.EndpointsGRPC()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	reference := storageRefConfig(endpoints[0])
	require.NoError(t, os.WriteFile(configPath, []byte(reference), 0o644))

	client := etcdClient(t, endpoints)
	etcdPut(t, client, "/test/config/all", "groups: {}\n")

	published := "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
		"          storage-1: {}\n"

	var checked []string

	ctx := clustercmd.PublishCtx{
		Force:      true,
		Publishers: sdkcluster.NewFactory(),
		Collectors: sdkcluster.NewFactory(),
		Src:        []byte(published),
		CheckTopology: func(topology []string) error {
			checked = topology

			return nil
		},
	}

	require.NoError(t, clustercmd.PublishApp(ctx, configPath, ""))

	assert.Equal(t, published, etcdGet(t, client, "/test/config/all"))
	assert.ElementsMatch(t, []string{"storage-1", "file-1"}, checked)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, reference, string(data), "the file keeps its reference")

	cfg, err := cluster.GetClusterConfig(context.Background(), configPath, integrity.IntegrityCtx{})
	require.NoError(t, err)

	topology, err := cluster.Instances(cfg.Snapshot())
	require.NoError(t, err)
	assert.Equal(t, []string{"file-1", "storage-1"}, topology)

	// The config of an instance is merged into what the storage holds.
	ctx.Src = []byte("iproto:\n  listen:\n    - uri: 127.0.0.1:3399\n")
	ctx.Config = map[string]any{
		"iproto": map[string]any{"listen": []any{map[string]any{"uri": "127.0.0.1:3399"}}},
	}

	require.NoError(t, clustercmd.PublishApp(ctx, configPath, "storage-1"))
	assert.Contains(t, etcdGet(t, client, "/test/config/all"), "127.0.0.1:3399")
}

// TestOpenConfigStorage pins when a config file points to a config storage
// tt publishes into.
func TestOpenConfigStorage(t *testing.T) {
	write := func(t *testing.T, config string) string {
		t.Helper()

		path := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte(config), 0o644))

		return path
	}

	factory := sdkcluster.NewFactory()

	t.Run("a file of the cluster config itself", func(t *testing.T) {
		_, found, err := cluster.OpenConfigStorage(context.Background(),
			write(t, "groups: {}\n"), factory, factory)
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("a reference without endpoints", func(t *testing.T) {
		_, found, err := cluster.OpenConfigStorage(context.Background(),
			write(t, "config:\n  etcd:\n    prefix: /test\n"), factory, factory)
		require.ErrorContains(t, err, "config.etcd of ")
		require.ErrorContains(t, err, "names no endpoints of the config storage")
		assert.False(t, found)
	})
}
