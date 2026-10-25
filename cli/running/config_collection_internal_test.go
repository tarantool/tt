package running

//nolint:depguard // The fixture serves etcd's wire protocol to count complete config loads.
import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/v3/cli/app"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// collectionConfigStorage implements only the etcd requests used to load a
// cluster config. Counting read transactions observes complete cluster loads without
// adding a test hook to the production loader.
type collectionConfigStorage struct {
	etcdserverpb.UnimplementedKVServer
	etcdserverpb.UnimplementedClusterServer

	reads atomic.Int32
	fail  bool
}

func (*collectionConfigStorage) MemberList(context.Context,
	*etcdserverpb.MemberListRequest,
) (*etcdserverpb.MemberListResponse, error) {
	return &etcdserverpb.MemberListResponse{}, nil
}

func (storage *collectionConfigStorage) Txn(context.Context,
	*etcdserverpb.TxnRequest,
) (*etcdserverpb.TxnResponse, error) {
	storage.reads.Add(1)

	if storage.fail {
		return nil, status.Error(codes.InvalidArgument, "config storage rejected the request")
	}

	return &etcdserverpb.TxnResponse{
		Succeeded: true,
		Responses: []*etcdserverpb.ResponseOp{{
			Response: &etcdserverpb.ResponseOp_ResponseRange{
				ResponseRange: &etcdserverpb.RangeResponse{
					Kvs: []*mvccpb.KeyValue{{
						Key:   []byte("/config/config/all"),
						Value: []byte("log:\n  level: info\n  format: json\n"),
					}},
				},
			},
		}},
	}, nil
}

func startCollectionConfigStorage(t *testing.T, storage *collectionConfigStorage) string {
	t.Helper()

	var listenerConfig net.ListenConfig

	listener, err := listenerConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	etcdserverpb.RegisterKVServer(server, storage)
	etcdserverpb.RegisterClusterServer(server, storage)

	done := make(chan error, 1)

	go func() { done <- server.Serve(listener) }()

	t.Cleanup(func() {
		server.Stop()
		require.NoError(t, <-done)
	})

	return listener.Addr().String()
}

func collectionConfigApp(t *testing.T, endpoint string, count int) (string, string) {
	t.Helper()

	appDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(appDir, ".templates/default"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "app.manifest.toml"),
		[]byte(fixtureManifest), 0o644))

	var instances, scopes strings.Builder

	for i := range count {
		name := fmt.Sprintf("instance-%d", i)
		fmt.Fprintf(&instances, "%s:\n", name)
		fmt.Fprintf(&scopes, "          %s:\n            console:\n              socket: %s.sock\n",
			name, name)
	}

	clusterConfig := fmt.Sprintf(`config:
  etcd:
    endpoints: [%q]
    prefix: /config
wal:
  dir: global-wal
log:
  level: debug
groups:
  group:
    snapshot:
      dir: group-snapshot
    replicasets:
      replicaset:
        vinyl:
          dir: replicaset-vinyl
        instances:
%s`, endpoint, scopes.String())

	require.NoError(t, os.WriteFile(filepath.Join(appDir, ".templates/default/instances.yaml"),
		[]byte(instances.String()), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, ".templates/default/config.yaml"),
		[]byte(clusterConfig), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "init.lua"), []byte("-- test"), 0o644))

	return appDir, clusterConfig
}

func collectionConfigString(t *testing.T, instance InstanceCtx, path string) string {
	t.Helper()
	require.NotNil(t, instance.Configuration)

	var value string

	_, err := instance.Configuration.Get(goconfig.NewKeyPath(path), &value)
	require.NoError(t, err, "%s: %s", instance.InstName, path)

	return value
}

func collectConfigApp(t *testing.T, appDir, selected string) ([]InstanceCtx, error) {
	t.Helper()

	var targets []app.Target

	if selected != "" {
		targets = []app.Target{{App: fixtureApp, Instance: selected}}
	}

	return Collect(t.Context(), targets, app.Options{Start: appDir, Scope: app.ScopeProject})
}

func TestCollectInstancesReusesClusterConfig(t *testing.T) {
	storage := &collectionConfigStorage{}
	endpoint := startCollectionConfigStorage(t, storage)
	appDir, _ := collectionConfigApp(t, endpoint, 20)
	t.Setenv("TT_WAL_MODE_DEFAULT", "write")
	t.Setenv("TT_LOG_LEVEL", "warn")

	before := storage.reads.Load()

	instances, err := collectConfigApp(t, appDir, "")
	require.NoError(t, err)
	require.Len(t, instances, 20)
	assert.Equal(t, before+1, storage.reads.Load())

	seen := make(map[string]bool)

	for _, instance := range instances {
		assert.False(t, seen[instance.InstName])

		seen[instance.InstName] = true

		assert.Equal(t, instance.InstName+".sock",
			collectionConfigString(t, instance, "console/socket"))
		assert.Equal(t, "write", collectionConfigString(t, instance, "wal/mode"))
		assert.Equal(t, "warn", collectionConfigString(t, instance, "log/level"))
		assert.Equal(t, "json", collectionConfigString(t, instance, "log/format"))
		assert.Equal(t, filepath.Join(appDir, "global-wal"), instance.WalDir)
		assert.Equal(t, filepath.Join(appDir, "group-snapshot"), instance.MemtxDir)
		assert.Equal(t, filepath.Join(appDir, "replicaset-vinyl"), instance.VinylDir)
		assert.Equal(t, filepath.Join(appDir, instance.InstName+".sock"),
			instance.ConsoleSocket)
	}
}

func TestCollectInstancesReloadsClusterConfig(t *testing.T) {
	storage := &collectionConfigStorage{}
	endpoint := startCollectionConfigStorage(t, storage)
	appDir, original := collectionConfigApp(t, endpoint, 2)

	instances, err := collectConfigApp(t, appDir, "")
	require.NoError(t, err)
	require.Len(t, instances, 2)
	assert.Equal(t, int32(1), storage.reads.Load())

	updated := strings.ReplaceAll(original, "global-wal", "changed-wal")
	require.NoError(t, os.WriteFile(filepath.Join(appDir, ".templates/default/config.yaml"),
		[]byte(updated), 0o644))

	newInstances, err := collectConfigApp(t, appDir, "")
	require.NoError(t, err)
	require.Len(t, newInstances, 2)
	assert.Equal(t, int32(2), storage.reads.Load())

	for _, instance := range instances {
		assert.Equal(t, "global-wal", collectionConfigString(t, instance, "wal/dir"))
	}

	for _, instance := range newInstances {
		assert.Equal(t, "changed-wal", collectionConfigString(t, instance, "wal/dir"))
	}
}

func TestCollectInstancesClusterConfigFailure(t *testing.T) {
	storage := &collectionConfigStorage{fail: true}
	endpoint := startCollectionConfigStorage(t, storage)
	appDir, _ := collectionConfigApp(t, endpoint, 20)

	instances, err := collectConfigApp(t, appDir, "")
	require.ErrorContains(t, err, "config storage rejected the request")
	assert.Empty(t, instances)
	assert.Equal(t, int32(1), storage.reads.Load())
}

func TestCollectInstancesClusterConfigSelection(t *testing.T) {
	storage := &collectionConfigStorage{}
	endpoint := startCollectionConfigStorage(t, storage)
	appDir, _ := collectionConfigApp(t, endpoint, 2)

	_, err := collectConfigApp(t, appDir, "missing")
	require.Error(t, err)
	// The resolver validates the complete pair before selecting an instance.
	assert.Equal(t, int32(1), storage.reads.Load())

	for _, description := range []string{"", "invalid: ["} {
		require.NoError(t, os.WriteFile(filepath.Join(appDir, ".templates/default/instances.yaml"),
			[]byte(description), 0o644))

		before := storage.reads.Load()

		_, err = collectConfigApp(t, appDir, "")
		require.Error(t, err)
		assert.Equal(t, before, storage.reads.Load())
	}
	require.NoError(t, os.Remove(filepath.Join(appDir, ".templates/default/instances.yaml")))
	before := storage.reads.Load()
	_, err = collectConfigApp(t, appDir, "")
	require.Error(t, err)
	assert.Equal(t, before, storage.reads.Load())
}

func TestCollectInstancesMalformedClusterConfig(t *testing.T) {
	storage := &collectionConfigStorage{}
	endpoint := startCollectionConfigStorage(t, storage)
	appDir, _ := collectionConfigApp(t, endpoint, 2)
	require.NoError(t, os.WriteFile(filepath.Join(appDir, ".templates/default/config.yaml"),
		[]byte("["), 0o644))

	instances, err := collectConfigApp(t, appDir, "")
	require.ErrorContains(t, err, "unable to load config")
	assert.Empty(t, instances)
	assert.Zero(t, storage.reads.Load())
}

func TestCollectInstancesWithoutClusterConfig(t *testing.T) {
	appDir, _ := collectionConfigApp(t, "unused", 2)
	require.NoError(t, os.Remove(filepath.Join(appDir, ".templates/default/config.yaml")))

	instances, err := collectConfigApp(t, appDir, "")
	require.Error(t, err)
	assert.Empty(t, instances)
}

func TestCollectInstancesSelectedClusterInstance(t *testing.T) {
	storage := &collectionConfigStorage{}
	endpoint := startCollectionConfigStorage(t, storage)
	appDir, _ := collectionConfigApp(t, endpoint, 2)

	instances, err := collectConfigApp(t, appDir, "instance-0")
	require.NoError(t, err)
	require.Len(t, instances, 1)
	assert.Equal(t, "instance-0", instances[0].InstName)
	assert.Equal(t, "instance-0.sock", collectionConfigString(t, instances[0], "console/socket"))
	assert.Equal(t, int32(1), storage.reads.Load())
	require.NoError(t, os.WriteFile(filepath.Join(appDir, ".templates/default/instances.yaml"),
		[]byte("instance-0:\nmissing:\n"), 0o644))

	for _, selected := range []string{"instance-0", "missing", ""} {
		before := storage.reads.Load()

		instances, err = collectConfigApp(t, appDir, selected)
		require.ErrorContains(t, err, "missing")
		assert.Empty(t, instances)
		assert.Equal(t, before+1, storage.reads.Load())
	}
}
