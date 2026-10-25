//go:build integration

package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	goconfig "github.com/tarantool/go-config/v2"
	etcdtest "github.com/tarantool/go-storage/v2/test_helpers/etcd"
	"github.com/tarantool/tt/v3/cli/app"
)

// thinConfig is a cluster config that holds nothing but where the rest of it
// is: the etcd at endpoint, under /myapp.
const thinConfig = `config:
  etcd:
    endpoints:
      - %s
    prefix: /myapp
    http:
      request:
        timeout: 2.5
`

// etcdTopology is the cluster config kept in etcd.
const etcdTopology = `groups:
  storages:
    replicasets:
      s-1:
        instances:
          storage-1:
            iproto:
              listen: [{uri: 127.0.0.1:3301}]
          storage-2: {}
`

func TestLoadThinConfig(t *testing.T) {
	etcd := etcdtest.New(t, etcdtest.ClusterConfig{Size: 1})
	endpoints := etcd.EndpointsGRPC()

	client, err := clientv3.New(clientv3.Config{Endpoints: endpoints})
	require.NoError(t, err)

	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Put(ctx, "/myapp/config/all", etcdTopology)
	require.NoError(t, err)

	roots(t)

	tree := t.TempDir()
	writeTree(t, tree, map[string]string{
		"app.manifest.toml":                 manifestOf("myapp"),
		".templates/default/config.yaml":    fmt.Sprintf(thinConfig, endpoints[0]),
		".templates/default/instances.yaml": "storage-1:\n",
	})

	got, err := app.Load(ctx, "", app.Options{Start: tree})
	require.NoError(t, err)
	assert.Equal(t, []string{"storage-1"}, names(got.Instances))

	var uri string

	snapshot := got.Config.Snapshot()

	_, err = snapshot.Get(goconfig.NewKeyPath(
		"groups/storages/replicasets/s-1/instances/storage-1/iproto/listen/0/uri"), &uri)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:3301", uri, "the topology comes from etcd")

	t.Run("an instance etcd does not declare", func(t *testing.T) {
		writeTree(t, tree, map[string]string{
			".templates/default/instances.yaml": "storage-1:\nstorage-3:\n",
		})

		_, err := app.Load(ctx, "", app.Options{Start: tree})
		requireFailure(t, err, app.ErrTopologyMismatch)
		require.ErrorContains(t, err, `"storage-3"`)
	})
}
