//go:build integration

package cluster_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	frameworkintegration "go.etcd.io/etcd/tests/v3/framework/integration"
	"go.etcd.io/etcd/tests/v3/integration"

	goconfig "github.com/tarantool/go-config"
	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/templates"
	"github.com/tarantool/tt/lib/integrity"
)

// spell-checker:ignore etcdmode etcddir etcdapp

const timeout = 5 * time.Second

type etcdOpts struct {
	Username string
	Password string
	KeyFile  string
	CertFile string
	CaFile   string
}

func doWithCtx(action func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return action(ctx)
}

func startEtcd(t *testing.T, opts etcdOpts) integration.LazyCluster {
	t.Helper()

	myDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %s", err)
	}

	var tls *transport.TLSInfo
	if opts.CaFile != "" || opts.CertFile != "" || opts.KeyFile != "" {
		tls = &transport.TLSInfo{}
		if opts.CaFile != "" {
			caPath := filepath.Join(myDir, opts.CaFile)
			tls.TrustedCAFile = caPath
		}
		if opts.CertFile != "" {
			certPath := filepath.Join(myDir, opts.CertFile)
			tls.CertFile = certPath
		}
		if opts.KeyFile != "" {
			keyPath := filepath.Join(myDir, opts.KeyFile)
			tls.KeyFile = keyPath
		}
	}
	config := frameworkintegration.ClusterConfig{Size: 1, PeerTLS: tls, UseTCP: true}
	inst := integration.NewLazyClusterWithConfig(config)

	if opts.Username != "" {
		etcd, err := clientv3.New(clientv3.Config{
			Endpoints: inst.EndpointsGRPC(),
		})
		require.NoError(t, err)
		defer etcd.Close()

		if err := doWithCtx(func(ctx context.Context) error {
			_, err := etcd.UserAdd(ctx, opts.Username, opts.Password)
			return err
		}); err != nil {
			inst.Terminate()
			t.Fatalf("Failed to create user in etcd: %s", err)
		}

		if opts.Username != "root" {
			// We need the root user for auth enable anyway.
			if err := doWithCtx(func(ctx context.Context) error {
				_, err := etcd.UserAdd(ctx, "root", "")
				return err
			}); err != nil {
				inst.Terminate()
				t.Fatalf("Failed to create root in etcd: %s", err)
			}

			if err := doWithCtx(func(ctx context.Context) error {
				_, err := etcd.UserGrantRole(ctx, "root", "root")
				return err
			}); err != nil {
				inst.Terminate()
				t.Fatalf("Failed to grant root in etcd: %s", err)
			}
		}

		if err := doWithCtx(func(ctx context.Context) error {
			_, err := etcd.UserGrantRole(ctx, opts.Username, "root")
			return err
		}); err != nil {
			inst.Terminate()
			t.Fatalf("Failed to grant user in etcd: %s", err)
		}

		if err := doWithCtx(func(ctx context.Context) error {
			_, err = etcd.AuthEnable(ctx)
			return err
		}); err != nil {
			inst.Terminate()
			t.Fatalf("Failed to enable auth in etcd: %s", err)
		}
	}

	return inst
}

func etcdPut(t *testing.T, etcd *clientv3.Client, key, value string) {
	t.Helper()
	var (
		pResp *clientv3.PutResponse
		err   error
	)
	doWithCtx(func(ctx context.Context) error {
		pResp, err = etcd.Put(ctx, key, value)
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, pResp)
}

func etcdGet(t *testing.T, etcd *clientv3.Client, key string) ([]byte, int64) {
	t.Helper()
	var (
		resp *clientv3.GetResponse
		err  error
	)
	doWithCtx(func(ctx context.Context) error {
		resp, err = etcd.Get(ctx, key)
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	if len(resp.Kvs) == 0 {
		return []byte(""), 0
	}
	require.Len(t, resp.Kvs, 1)
	return resp.Kvs[0].Value, resp.Kvs[0].ModRevision
}

func renderEtcdAppConfig(t *testing.T, endpoint, src, dst string) {
	engine := templates.NewDefaultEngine()
	err := engine.RenderFile(src, dst, map[string]string{
		"endpoint": endpoint,
	})
	require.NoError(t, err)
}

// cfgGet retrieves a typed value from cfg at the given slash-separated path.
func cfgGet[T any](t *testing.T, cfg goconfig.Config, path string) T {
	t.Helper()
	var v T
	_, err := cfg.Get(goconfig.NewKeyPath(path), &v)
	require.NoError(t, err, "path: %s", path)
	return v
}

func TestGetClusterConfig_etcd(t *testing.T) {
	inst := startEtcd(t, etcdOpts{
		Username: "root",
		Password: "pass",
	})
	defer inst.Terminate()
	endpoints := inst.EndpointsGRPC()

	tmpDir, err := os.MkdirTemp("", "work_dir")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	renderEtcdAppConfig(t, endpoints[0], "testdata/etcdapp/config.yaml.template", configPath)

	etcd, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
		Username:  "root",
		Password:  "pass",
	})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/test/config/all", `wal:
  dir: etcddir
  mode: etcdmode
`)
	t.Setenv("TT_WAL_MODE_DEFAULT", "envmode")
	t.Setenv("TT_WAL_MAX_SIZE_DEFAULT", "envsize")

	cfg, err := cluster.GetClusterConfig(context.Background(), configPath, integrity.IntegrityCtx{})

	require.NoError(t, err)
	require.NotNil(t, cfg)

	snap := cfg.Snapshot()

	// App fields from file.
	assert.Equal(t, 1, cfgGet[int](t, snap, "app/foo"))
	assert.Equal(t, 1, cfgGet[int](t, snap, "app/bar"))
	assert.Equal(t, 1, cfgGet[int](t, snap, "app/zoo"))
	assert.Equal(t, 1, cfgGet[int](t, snap, "app/hoo"))

	// Etcd config from file.
	assert.Equal(t, endpoints[0], cfgGet[string](t, snap,
		fmt.Sprintf("config/etcd/endpoints/0")))
	assert.Equal(t, "root", cfgGet[string](t, snap, "config/etcd/username"))
	assert.Equal(t, "pass", cfgGet[string](t, snap, "config/etcd/password"))
	assert.Equal(t, "/test", cfgGet[string](t, snap, "config/etcd/prefix"))

	// wal/dir from file (file > etcd per priority).
	assert.Equal(t, "filedir", cfgGet[string](t, snap, "wal/dir"))
	// wal/mode from etcd (not in file).
	assert.Equal(t, "etcdmode", cfgGet[string](t, snap, "wal/mode"))
	// wal/max_size from TT_WAL_MAX_SIZE_DEFAULT env (lowest priority, fills in missing).
	assert.Equal(t, "envsize", cfgGet[string](t, snap, "wal/max_size"))

	// Groups from file.
	assert.Equal(t, 2, cfgGet[int](t, snap, "groups/a/foo"))
	assert.Equal(t, 4, cfgGet[int](t, snap, "groups/a/replicasets/b/instances/c/foo"))
}

func TestGetClusterConfig_etcd_connect_from_env(t *testing.T) {
	const (
		user   = "userenv"
		pass   = "passenv"
		prefix = "/prefixenv"
	)

	inst := startEtcd(t, etcdOpts{
		Username: user,
		Password: pass,
	})
	defer inst.Terminate()
	endpoints := inst.EndpointsGRPC()

	tmpDir, err := os.MkdirTemp("", "work_dir")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	renderEtcdAppConfig(t, endpoints[0], "testdata/etcdapp/config.yaml.template", configPath)

	etcd, err := clientv3.New(clientv3.Config{
		Endpoints: endpoints,
		Username:  user,
		Password:  pass,
	})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, prefix+"/config/all", `wal:
  dir: etcddir
  mode: etcdmode
`)
	t.Setenv("TT_CONFIG_ETCD_USERNAME", user)
	t.Setenv("TT_CONFIG_ETCD_PASSWORD", pass)
	t.Setenv("TT_CONFIG_ETCD_PREFIX", prefix)

	cfg, err := cluster.GetClusterConfig(context.Background(), configPath, integrity.IntegrityCtx{})

	require.NoError(t, err)
	require.NotNil(t, cfg)

	snap := cfg.Snapshot()

	// Credentials from env.
	assert.Equal(t, user, cfgGet[string](t, snap, "config/etcd/username"))
	assert.Equal(t, pass, cfgGet[string](t, snap, "config/etcd/password"))
	assert.Equal(t, prefix, cfgGet[string](t, snap, "config/etcd/prefix"))

	// wal/dir from file (file > etcd).
	assert.Equal(t, "filedir", cfgGet[string](t, snap, "wal/dir"))
	// wal/mode from etcd (not in file).
	assert.Equal(t, "etcdmode", cfgGet[string](t, snap, "wal/mode"))
}
