//go:build integration

package cluster_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/integrity"
)

const (
	baseEndpoint  = "127.0.0.1:12379"
	httpEndpoint  = "http://" + baseEndpoint
	httpsEndpoint = "https://" + baseEndpoint
	timeout       = 5 * time.Second
)

type etcdOpts struct {
	Username string
	Password string
	KeyFile  string
	CertFile string
	CaFile   string
}

type etcdInstance struct {
	Cmd *exec.Cmd
	Dir string
}

func startEtcd(t *testing.T, endpoint string, opts etcdOpts) etcdInstance {
	t.Helper()

	mydir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %s", err)
	}

	inst := etcdInstance{}
	dir, err := os.MkdirTemp("", "work_dir")
	if err != nil {
		t.Fatalf("Failed to create a temporary directory: %s", err)
	}
	inst.Dir = dir
	inst.Cmd = exec.Command("etcd")

	inst.Cmd.Env = append(
		os.Environ(),
		fmt.Sprintf("ETCD_LISTEN_CLIENT_URLS=%s", endpoint),
		fmt.Sprintf("ETCD_ADVERTISE_CLIENT_URLS=%s", endpoint),
		fmt.Sprintf("ETCD_DATA_DIR=%s", inst.Dir),
	)
	if opts.KeyFile != "" {
		keyPath := filepath.Join(mydir, opts.KeyFile)
		inst.Cmd.Env = append(inst.Cmd.Env,
			fmt.Sprintf("ETCD_KEY_FILE=%s", keyPath))
	}
	if opts.CertFile != "" {
		certPath := filepath.Join(mydir, opts.CertFile)
		inst.Cmd.Env = append(inst.Cmd.Env,
			fmt.Sprintf("ETCD_CERT_FILE=%s", certPath))
	}
	if opts.CaFile != "" {
		caPath := filepath.Join(mydir, opts.CaFile)
		inst.Cmd.Env = append(inst.Cmd.Env,
			fmt.Sprintf("ETCD_TRUSTED_CA_FILE=%s", caPath))
	}

	// Start etcd.
	err = inst.Cmd.Start()
	if err != nil {
		os.RemoveAll(inst.Dir)
		t.Fatalf("Failed to start etcd: %s", err)
	}

	// Setup user/pass.
	if opts.Username != "" {
		cmd := exec.Command("etcdctl", "user", "add", opts.Username,
			fmt.Sprintf("--new-user-password=%s", opts.Password),
			fmt.Sprintf("--endpoints=%s", baseEndpoint))

		err := cmd.Run()
		if err != nil {
			stopEtcd(t, inst)
			t.Fatalf("Failed to create user: %s", err)
		}

		if opts.Username != "root" {
			// We need the root user for auth enable anyway.
			cmd := exec.Command("etcdctl", "user", "add", "root",
				fmt.Sprintf("--new-user-password=%s", opts.Password),
				fmt.Sprintf("--endpoints=%s", baseEndpoint))

			err := cmd.Run()
			if err != nil {
				stopEtcd(t, inst)
				t.Fatalf("Failed to create root: %s", err)
			}

			// And additional permissions for a regular user.
			cmd = exec.Command("etcdctl", "user", "grant-role", opts.Username,
				"root", fmt.Sprintf("--endpoints=%s", baseEndpoint))

			err = cmd.Run()
			if err != nil {
				stopEtcd(t, inst)
				t.Fatalf("Failed to grant-role: %s", err)
			}
		}

		cmd = exec.Command("etcdctl", "auth", "enable",
			fmt.Sprintf("--user=root:%s", opts.Password),
			fmt.Sprintf("--endpoints=%s", baseEndpoint))

		err = cmd.Run()
		if err != nil {
			stopEtcd(t, inst)
			t.Fatalf("Failed to enable auth: %s", err)
		}
	}

	return inst
}

func stopEtcd(t *testing.T, inst etcdInstance) {
	t.Helper()

	if inst.Cmd != nil && inst.Cmd.Process != nil {
		if err := inst.Cmd.Process.Kill(); err != nil {
			t.Fatalf("Failed to kill etcd (%d) %s", inst.Cmd.Process.Pid, err)
		}

		// Wait releases any resources associated with the Process.
		if _, err := inst.Cmd.Process.Wait(); err != nil {
			t.Fatalf("Failed to wait for etcd process to exit, got %s", err)
			return
		}

		inst.Cmd.Process = nil
	}

	if inst.Dir != "" {
		if err := os.RemoveAll(inst.Dir); err != nil {
			t.Fatalf("Failed to clean work directory, got %s", err)
		}
	}
}

func etcdPut(t *testing.T, etcd *clientv3.Client, key, value string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	presp, err := etcd.Put(ctx, key, value)
	cancel()
	require.NoError(t, err)
	require.NotNil(t, presp)
}

func etcdGet(t *testing.T, etcd *clientv3.Client, key string) ([]byte, int64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	resp, err := etcd.Get(ctx, key)
	cancel()

	require.NoError(t, err)
	require.NotNil(t, resp)
	if len(resp.Kvs) == 0 {
		return []byte(""), 0
	}

	require.Len(t, resp.Kvs, 1)
	return resp.Kvs[0].Value, resp.Kvs[0].ModRevision
}

type connectEtcdOpts struct {
	ServerEndpoint string
	ServerOpts     etcdOpts
	ClientEndpoint string
	ClientOpts     cluster.EtcdOpts
}

func TestConnectEtcd(t *testing.T) {
	cases := []struct {
		Name string
		Opts connectEtcdOpts
	}{
		{
			Name: "base",
			Opts: connectEtcdOpts{
				ServerEndpoint: httpEndpoint,
				ServerOpts:     etcdOpts{},
				ClientEndpoint: httpEndpoint,
				ClientOpts: cluster.EtcdOpts{
					Endpoints: []string{httpEndpoint},
				},
			},
		},
		{
			Name: "auth",
			Opts: connectEtcdOpts{
				ServerEndpoint: httpEndpoint,
				ServerOpts: etcdOpts{
					Username: "root",
					Password: "pass",
				},
				ClientEndpoint: httpEndpoint,
				ClientOpts: cluster.EtcdOpts{
					Endpoints: []string{httpEndpoint},
					Username:  "root",
					Password:  "pass",
				},
			},
		},
		{
			Name: "tls_ca_file",
			Opts: connectEtcdOpts{
				ServerEndpoint: httpsEndpoint,
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
				},
				ClientEndpoint: httpsEndpoint,
				ClientOpts: cluster.EtcdOpts{
					Endpoints: []string{httpsEndpoint},
					CaFile:    "testdata/tls/ca.crt",
				},
			},
		},
		{
			Name: "tls_ca_path",
			Opts: connectEtcdOpts{
				ServerEndpoint: httpsEndpoint,
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
				},
				ClientEndpoint: httpsEndpoint,
				ClientOpts: cluster.EtcdOpts{
					Endpoints: []string{httpsEndpoint},
					CaPath:    "testdata/tls/",
				},
			},
		},
		{
			Name: "tls_ca_skip_verify",
			Opts: connectEtcdOpts{
				ServerEndpoint: httpsEndpoint,
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
				},
				ClientEndpoint: httpsEndpoint,
				ClientOpts: cluster.EtcdOpts{
					Endpoints:      []string{httpsEndpoint},
					SkipHostVerify: true,
				},
			},
		},
		{
			Name: "tls_full",
			Opts: connectEtcdOpts{
				ServerEndpoint: httpsEndpoint,
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
					CaFile:   "testdata/tls/ca.crt",
				},
				ClientEndpoint: httpsEndpoint,
				ClientOpts: cluster.EtcdOpts{
					Endpoints: []string{httpsEndpoint},
					KeyFile:   "testdata/tls/localhost.key",
					CertFile:  "testdata/tls/localhost.crt",
					CaFile:    "testdata/tls/ca.crt",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			inst := startEtcd(t, tc.Opts.ServerEndpoint, tc.Opts.ServerOpts)
			defer stopEtcd(t, inst)

			etcd, err := cluster.ConnectEtcd(tc.Opts.ClientOpts)
			require.NoError(t, err)
			require.NotNil(t, etcd)
			defer etcd.Close()

			etcdPut(t, etcd, "foo", "bar")

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			gresp, err := etcd.Get(ctx, "foo")
			cancel()
			require.NoError(t, err)
			require.NotNil(t, gresp)
		})
	}
}

func TestConnectEtcd_invalid_endpoint(t *testing.T) {
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{
		Endpoints: []string{"some_unknown_endpoint:2323"},
		Timeout:   1 * time.Second,
	})

	assert.Nil(t, etcd)
	assert.ErrorContains(t, err, "context deadline exceeded")
}

func TestEtcdCollectors_single(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/foo/config/bar", "foo: bar")

	cases := []struct {
		Name      string
		Collector cluster.Collector
	}{
		{"all", cluster.NewYamlCollectorDecorator(
			cluster.NewEtcdAllCollector(etcd, "/foo/", timeout))},
		{"key", cluster.NewYamlCollectorDecorator(
			cluster.NewEtcdKeyCollector(etcd, "/foo/", "bar", timeout))},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()
			require.NoError(t, err)
			value, err := config.Get([]string{"foo"})
			require.NoError(t, err)
			assert.Equal(t, "bar", value)
		})
	}
}

func TestEtcdAllCollector_merge(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/foo/config/a", "foo: bar")
	etcdPut(t, etcd, "/foo/config/b", "foo: car\nzoo: car")

	config, err := cluster.NewYamlCollectorDecorator(
		cluster.NewEtcdAllCollector(etcd, "/foo/", timeout)).Collect()
	require.NoError(t, err)
	value, err := config.Get([]string{"foo"})
	require.NoError(t, err)
	assert.Equal(t, "bar", value)
	value, err = config.Get([]string{"zoo"})
	require.NoError(t, err)
	assert.Equal(t, "car", value)
}

func TestEtcdCollectors_empty(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	cases := []struct {
		Name      string
		Collector cluster.Collector
	}{
		{"all", cluster.NewYamlCollectorDecorator(
			cluster.NewEtcdAllCollector(etcd, "/foo/", timeout))},
		{"key", cluster.NewYamlCollectorDecorator(
			cluster.NewEtcdKeyCollector(etcd, "/foo/", "bar", timeout))},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()
			assert.Nil(t, config)
			assert.Error(t, err)
		})
	}
}

func TestGetClusterConfig_etcd(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{
		Username: "root",
		Password: "pass",
	})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{
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
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "testdata/etcdapp/config.yaml")
	os.Unsetenv("TT_WAL_MODE_DEFAULT")
	os.Unsetenv("TT_WAL_MAX_SIZE_DEFAULT")

	require.NoError(t, err)
	assert.Equal(t, `app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
config:
  etcd:
    endpoints:
      - http://127.0.0.1:12379
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
`, config.RawConfig.String())
}

func TestGetClusterConfig_etcd_connect_from_env(t *testing.T) {
	const (
		user   = "userenv"
		pass   = "passenv"
		prefix = "/prefixenv"
	)

	inst := startEtcd(t, httpEndpoint, etcdOpts{
		Username: user,
		Password: pass,
	})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{
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

	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "testdata/etcdapp/config.yaml")

	os.Unsetenv("TT_CONFIG_ETCD_USERNAME")
	os.Unsetenv("TT_CONFIG_ETCD_PASSWORD")
	os.Unsetenv("TT_CONFIG_ETCD_PREFIX")

	require.NoError(t, err)
	assert.Equal(t, `app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
config:
  etcd:
    endpoints:
      - http://127.0.0.1:12379
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
`, config.RawConfig.String())
}

func TestEtcdDataPublishers_Publish_single(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	data := []byte("foo bar")
	cases := []struct {
		Name      string
		Key       string
		Publisher integrity.DataPublisher
	}{
		{"all", "all", cluster.NewEtcdAllDataPublisher(etcd, "/foo/", timeout)},
		{"key", "key", cluster.NewEtcdKeyDataPublisher(etcd, "/foo/", "key", timeout)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err = tc.Publisher.Publish(0, data)

			assert.NoError(t, err)
			actual, _ := etcdGet(t, etcd, "/foo/config/"+tc.Key)
			assert.Equal(t, data, actual)
		})
	}
}

func TestEtcdDataPublishers_Publish_rewrite(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	oldData := []byte("foo bar zoo")
	newData := []byte("zoo bar foo")
	cases := []struct {
		Name      string
		Key       string
		Publisher integrity.DataPublisher
	}{
		{"all", "all", cluster.NewEtcdAllDataPublisher(etcd, "/foo/", timeout)},
		{"key", "key", cluster.NewEtcdKeyDataPublisher(etcd, "/foo/", "key", timeout)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err = tc.Publisher.Publish(0, oldData)
			require.NoError(t, err)
			err = tc.Publisher.Publish(0, newData)
			assert.NoError(t, err)
			actual, _ := etcdGet(t, etcd, "/foo/config/"+tc.Key)
			assert.Equal(t, newData, actual)
		})
	}
}

func TestEtcdAllDataPublisher_Publish_rewrite_prefix(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/foo/config/", "foo")
	etcdPut(t, etcd, "/foo/config/foo", "zoo")

	data := []byte("zoo bar foo")
	err = cluster.NewEtcdAllDataPublisher(etcd, "/foo/", timeout).Publish(0, data)

	assert.NoError(t, err)

	actual, _ := etcdGet(t, etcd, "/foo/config/")
	assert.Equal(t, []byte(""), actual)

	actual, _ = etcdGet(t, etcd, "/foo/config/foo")
	assert.Equal(t, []byte(""), actual)

	actual, _ = etcdGet(t, etcd, "/foo/config/all")
	assert.Equal(t, data, actual)
}

func TestEtcdKeyDataPublisher_Publish_modRevision_specified(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/foo/config/key", "bar")
	_, modRevision := etcdGet(t, etcd, "/foo/config/key")

	data := []byte("baz")

	publisher := cluster.NewEtcdKeyDataPublisher(etcd, "/foo", "key", timeout)
	// Use wrong revision.
	err = publisher.Publish(modRevision-1, data)
	assert.Errorf(t, err, "failed to put data into etcd: wrong revision")
	actual, _ := etcdGet(t, etcd, "/foo/config/key")
	assert.Equal(t, []byte("bar"), actual)

	// Use right revision.
	err = publisher.Publish(modRevision, data)
	assert.NoError(t, err)
	actual, _ = etcdGet(t, etcd, "/foo/config/key")
	assert.Equal(t, data, actual)
}

func TestEtcdKeyDataPublisher_Publish_ignore_prefix(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/foo/config/", "foo")
	etcdPut(t, etcd, "/foo/config/foo", "zoo")

	data := []byte("zoo bar foo")
	err = cluster.NewEtcdKeyDataPublisher(etcd, "/foo/", "all", timeout).Publish(0, data)

	assert.NoError(t, err)

	actual, _ := etcdGet(t, etcd, "/foo/config/")
	assert.Equal(t, []byte("foo"), actual)

	actual, _ = etcdGet(t, etcd, "/foo/config/foo")
	assert.Equal(t, []byte("zoo"), actual)

	actual, _ = etcdGet(t, etcd, "/foo/config/all")
	assert.Equal(t, data, actual)
}

func TestEtcdAllDataPublisher_collect_publish_collect(t *testing.T) {
	inst := startEtcd(t, httpEndpoint, etcdOpts{})
	defer stopEtcd(t, inst)

	endpoints := []string{httpEndpoint}
	etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{Endpoints: endpoints})
	require.NoError(t, err)
	require.NotNil(t, etcd)
	defer etcd.Close()

	etcdPut(t, etcd, "/foo/config/foo", "zoo: bar")

	prefix := "/foo/"
	dataPublisher := cluster.NewEtcdAllDataPublisher(etcd, prefix, timeout)
	publisher := cluster.NewYamlConfigPublisher(dataPublisher)
	collector := cluster.NewYamlCollectorDecorator(
		cluster.NewEtcdAllCollector(etcd, prefix, timeout))

	config, err := collector.Collect()
	require.NoError(t, err)
	value, err := config.Get([]string{"zoo"})
	assert.NoError(t, err)
	assert.Equal(t, value, "bar")

	config = cluster.NewConfig()
	config.Set([]string{"foo"}, "bar")

	err = publisher.Publish(config)
	assert.NoError(t, err)
	config, err = collector.Collect()
	require.NoError(t, err)

	value, err = config.Get([]string{"foo"})
	assert.NoError(t, err)
	assert.Equal(t, value, "bar")
	_, err = config.Get([]string{"zoo"})
	assert.Error(t, err)
}
