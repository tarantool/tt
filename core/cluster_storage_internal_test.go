//go:build integration

package core

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	etcdtest "github.com/tarantool/go-storage/v2/test_helpers/etcd"

	"github.com/tarantool/tt/sdk"
	sdkcluster "github.com/tarantool/tt/sdk/cluster"
	"github.com/tarantool/tt/sdk/connect"
)

// TestServicesClusterConfigStorage checks the cluster configuration of an
// etcd: what it holds under the prefix or at a key, and nothing there.
func TestServicesClusterConfigStorage(t *testing.T) {
	t.Parallel()

	etcd := etcdtest.NewLazyCluster(etcdtest.ClusterConfig{Size: 1, PeerTLS: nil})
	t.Cleanup(etcd.Terminate)

	uri := etcd.EndpointsGRPC()[0] + "/prefix"

	opts, err := connect.CreateURIOpts(uri)
	require.NoError(t, err)

	stor, cleanup, storageType, err := sdkcluster.NewStorageConnection(
		sdkcluster.ConnectOpts{Username: "", Password: ""}, opts)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	raw, err := sdkcluster.NewFactory().NewRemoteStorage(stor, opts.Prefix, "",
		opts.Timeout, storageType)
	require.NoError(t, err)

	svc := readyServices(strings.NewReader(""), io.Discard, io.Discard)
	noCreds := sdk.Credentials{Username: "", Password: ""}

	_, err = svc.ClusterConfig(t.Context(), sdk.StorageSource(uri, noCreds))
	require.ErrorIs(t, err, sdk.ErrNotFound, "nothing under the prefix")

	require.NoError(t, raw.Put(t.Context(), "all", `
groups:
  g:
    replicasets:
      r:
        database:
          mode: ro
        instances:
          i: {}
`))

	for _, source := range []string{uri, uri + "?key=all"} {
		cfg, err := svc.ClusterConfig(t.Context(), sdk.StorageSource(source, noCreds))
		require.NoError(t, err, source)
		assert.Empty(t, cfg.Dir, source)

		instance, err := sdk.InstanceConfig(cfg.Config, "i")
		require.NoError(t, err, source)

		var mode string

		_, err = instance.Get([]string{"database", "mode"}, &mode)
		require.NoError(t, err, source)
		assert.Equal(t, "ro", mode, source)
	}

	_, err = svc.ClusterConfig(t.Context(), sdk.StorageSource(uri+"?key=nosuch", noCreds))
	require.ErrorIs(t, err, sdk.ErrNotFound, "nothing at the key")
}
