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
	"go.etcd.io/etcd/tests/v3/integration"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/templates"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

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

	mydir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %s", err)
	}

	var tls *transport.TLSInfo
	if opts.CaFile != "" || opts.CertFile != "" || opts.KeyFile != "" {
		tls = &transport.TLSInfo{}
		if opts.CaFile != "" {
			caPath := filepath.Join(mydir, opts.CaFile)
			tls.TrustedCAFile = caPath
		}
		if opts.CertFile != "" {
			certPath := filepath.Join(mydir, opts.CertFile)
			tls.CertFile = certPath
		}
		if opts.KeyFile != "" {
			keyPath := filepath.Join(mydir, opts.KeyFile)
			tls.KeyFile = keyPath
		}
	}
	config := integration.ClusterConfig{Size: 1, PeerTLS: tls, UseTCP: true}
	inst := integration.NewLazyClusterWithConfig(config)

	if opts.Username != "" {
		etcd, err := libcluster.ConnectEtcd(libcluster.EtcdOpts{
			Endpoints: inst.EndpointsV3(),
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
		presp *clientv3.PutResponse
		err   error
	)
	doWithCtx(func(ctx context.Context) error {
		presp, err = etcd.Put(ctx, key, value)
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, presp)
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

func renderEtcdAppConfig(t *testing.T, endpoint string, src string, dst string) {
	engine := templates.NewDefaultEngine()
	err := engine.RenderFile(src, dst, map[string]string{
		"endpoint": endpoint,
	})
	require.NoError(t, err)
}

func TestGetClusterConfig_etcd(t *testing.T) {
	inst := startEtcd(t, etcdOpts{
		Username: "root",
		Password: "pass",
	})
	defer inst.Terminate()
	endpoints := inst.EndpointsV3()

	tmpDir, err := os.MkdirTemp("", "work_dir")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	renderEtcdAppConfig(t, endpoints[0], "testdata/etcdapp/config.yaml.template", configPath)

	etcd, err := libcluster.ConnectEtcd(libcluster.EtcdOpts{
		Endpoints: endpoints,
		Username:  "root",
		Password:  "pass",
	})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/test/config/", `wal:
  dir: etcddir
  mode: etcdmode
`)
	os.Setenv("TT_WAL_MODE_DEFAULT", "envmode")
	os.Setenv("TT_WAL_MAX_SIZE_DEFAULT", "envsize")
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, configPath)
	os.Unsetenv("TT_WAL_MODE_DEFAULT")
	os.Unsetenv("TT_WAL_MAX_SIZE_DEFAULT")

	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(`app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
config:
  etcd:
    endpoints:
      - %s
    http:
      request:
        timeout: 2.5
    password: pass
    prefix: /test
    username: root
groups:
  a:
    bar: 2
    foo: 2
    replicasets:
      b:
        bar: 3
        foo: 3
        instances:
          c:
            foo: 4
    zoo: 2
  b:
    replicasets:
      b:
        instances:
          b:
            too: 3
        too: 3
    too: 2
wal:
  dir: filedir
  max_size: envsize
  mode: etcdmode
`, endpoints[0]), config.RawConfig.String())
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
	endpoints := inst.EndpointsV3()

	tmpDir, err := os.MkdirTemp("", "work_dir")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	renderEtcdAppConfig(t, endpoints[0], "testdata/etcdapp/config.yaml.template", configPath)

	etcd, err := libcluster.ConnectEtcd(libcluster.EtcdOpts{
		Endpoints: endpoints,
		Username:  user,
		Password:  pass,
	})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, prefix+"/config/", `wal:
  dir: etcddir
  mode: etcdmode
`)
	os.Setenv("TT_CONFIG_ETCD_USERNAME", user)
	os.Setenv("TT_CONFIG_ETCD_PASSWORD", pass)
	os.Setenv("TT_CONFIG_ETCD_PREFIX", prefix)

	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, configPath)

	os.Unsetenv("TT_CONFIG_ETCD_USERNAME")
	os.Unsetenv("TT_CONFIG_ETCD_PASSWORD")
	os.Unsetenv("TT_CONFIG_ETCD_PREFIX")

	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(`app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
config:
  etcd:
    endpoints:
      - %s
    http:
      request:
        timeout: 2.5
    password: passenv
    prefix: /prefixenv
    username: userenv
groups:
  a:
    bar: 2
    foo: 2
    replicasets:
      b:
        bar: 3
        foo: 3
        instances:
          c:
            foo: 4
    zoo: 2
  b:
    replicasets:
      b:
        instances:
          b:
            too: 3
        too: 3
    too: 2
wal:
  dir: filedir
  mode: etcdmode
`, endpoints[0]), config.RawConfig.String())
}
