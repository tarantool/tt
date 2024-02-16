//go:build integration

package cluster_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"

	"github.com/tarantool/tt/lib/cluster"
)

const (
	baseEndpoint  = "127.0.0.1:12379"
	httpEndpoint  = "http://" + baseEndpoint
	httpsEndpoint = "https://" + baseEndpoint
	timeout       = 5 * time.Second
)

func tcsIsSupported(t *testing.T) bool {
	cmd := exec.Command("tarantool", "--version")

	out, err := cmd.Output()
	require.NoError(t, err)

	expected := "Tarantool Enterprise 3"

	return strings.HasPrefix(string(out), expected)
}

func startTcs(t *testing.T) *exec.Cmd {
	cmd := exec.Command("tarantool", "--name", "master",
		"--config", "testdata/config.yml",
		"testdata/init.lua")
	err := cmd.Start()
	require.NoError(t, err)

	var conn tarantool.Connector
	// Wait for Tarantool to start.
	for i := 0; i < 10; i++ {
		conn, err = tarantool.Connect("127.0.0.1:3301", tarantool.Opts{})
		if err == nil {
			defer conn.Close()
			break
		}
		time.Sleep(time.Second)
	}
	require.NoError(t, err)

	return cmd
}

func stopTcs(t *testing.T, cmd *exec.Cmd) {
	err := cmd.Process.Kill()
	require.NoError(t, err)
}

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
		Publisher cluster.DataPublisher
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
		Publisher cluster.DataPublisher
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
	require.NoError(t, err)

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

func TestEtcdAllDataPublisher_Publish_ignore_prefix(t *testing.T) {
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

var testsIntegrity = []struct {
	Name         string
	Applicable   func(t *testing.T) bool
	Setup        func(t *testing.T) interface{}
	Shutdown     func(t *testing.T, inst interface{})
	NewPublisher func(
		t *testing.T,
		signFunc cluster.SignFunc,
		prefix, key string,
	) (cluster.DataPublisher, func())
	NewCollector func(
		t *testing.T,
		checkFunc cluster.CheckFunc,
		prefix, key string,
	) (cluster.DataCollector, func())
}{
	{
		Name:       "tarantool",
		Applicable: tcsIsSupported,
		Setup: func(t *testing.T) interface{} {
			command := startTcs(t)
			return command
		},
		Shutdown: func(t *testing.T, inst interface{}) {
			stopTcs(t, inst.(*exec.Cmd))
		},
		NewPublisher: func(
			t *testing.T,
			signFunc cluster.SignFunc,
			prefix,
			key string,
		) (cluster.DataPublisher, func()) {
			publisherFactory := cluster.NewIntegrityDataPublisherFactory(signFunc)

			opts := tarantool.Opts{
				Timeout:       10 * time.Second,
				Reconnect:     10 * time.Second,
				MaxReconnects: 10,
				User:          "client",
				Pass:          "secret",
			}

			conn, err := tarantool.Connect("127.0.0.1:3301", opts)
			require.NoError(t, err)

			pub, err := publisherFactory.NewTarantool(conn, prefix, key, 1*time.Second)
			require.NoError(t, err)

			return pub, func() { conn.Close() }
		},
		NewCollector: func(
			t *testing.T,
			checkFunc cluster.CheckFunc,
			prefix,
			key string,
		) (cluster.DataCollector, func()) {
			collectorFactory := cluster.NewIntegrityDataCollectorFactory(checkFunc, nil)

			opts := tarantool.Opts{
				Timeout:       10 * time.Second,
				Reconnect:     10 * time.Second,
				MaxReconnects: 10,
				User:          "client",
				Pass:          "secret",
			}

			conn, err := tarantool.Connect("127.0.0.1:3301", opts)
			require.NoError(t, err)

			coll, err := collectorFactory.NewTarantool(conn, prefix, key, 1*time.Second)
			require.NoError(t, err)

			return coll, func() { conn.Close() }
		},
	},
	{
		Name:       "etcd",
		Applicable: func(t *testing.T) bool { return true },
		Setup: func(t *testing.T) interface{} {
			inst := startEtcd(t, httpEndpoint, etcdOpts{})
			return inst
		},
		Shutdown: func(t *testing.T, inst interface{}) {
			stopEtcd(t, inst.(etcdInstance))
		},
		NewPublisher: func(
			t *testing.T,
			signFunc cluster.SignFunc,
			prefix,
			key string,
		) (cluster.DataPublisher, func()) {
			publisherFactory := cluster.NewIntegrityDataPublisherFactory(signFunc)

			etcd, err := clientv3.New(clientv3.Config{
				Endpoints:   []string{httpEndpoint},
				DialTimeout: 1 * time.Second,
			})
			require.NoError(t, err)

			pub, err := publisherFactory.NewEtcd(etcd, prefix, key, 10*time.Second)
			require.NoError(t, err)

			return pub, func() { etcd.Close() }
		},
		NewCollector: func(
			t *testing.T,
			checkFunc cluster.CheckFunc,
			prefix,
			key string,
		) (cluster.DataCollector, func()) {
			collectorFactory := cluster.NewIntegrityDataCollectorFactory(checkFunc, nil)

			etcd, err := clientv3.New(clientv3.Config{
				Endpoints:   []string{httpEndpoint},
				DialTimeout: 60 * time.Second,
			})
			require.NoError(t, err)

			coll, err := collectorFactory.NewEtcd(etcd, prefix, key, 10*time.Second)
			require.NoError(t, err)

			return coll, func() { etcd.Close() }
		},
	},
}

var validSignFunc = func(data []byte) (map[string][]byte, []byte, error) {
	return map[string][]byte{
		"foo": []byte("foo"),
		"bar": data,
	}, data, nil
}

var validCheckFunc = func(data []byte, hashes map[string][]byte, sig []byte) error {
	expected := map[string][]byte{
		"foo": []byte("foo"),
		"bar": data,
	}
	if !reflect.DeepEqual(expected, hashes) {
		return fmt.Errorf("an unexpected hashes map: %q, expected: %q", hashes, expected)
	}

	if string(data) != string(sig) {
		return fmt.Errorf("data %q != sig %q", string(data), string(sig))
	}
	return nil
}

func TestIntegrityDataPublisherKey_CollectorAll_valid(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test1"

			data := []cluster.Data{
				{"bar", []byte("abcdefg"), 0},
				{"baz", []byte("qwertyuiop"), 0},
			}

			for _, entry := range data {
				publisher, closeConn :=
					test.NewPublisher(t, validSignFunc, testPrefix, entry.Source)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			collector, closeConn := test.NewCollector(t, validCheckFunc, testPrefix, "")
			defer closeConn()

			result, err := collector.Collect()
			require.NoError(t, err)

			require.Equal(t, result, []cluster.Data{
				{testPrefix + "/config/bar", data[0].Value, 0},
				{testPrefix + "/config/baz", data[1].Value, 0},
			})
		})
	}
}

func TestIntegrityDataPublisherKey_CollectorKey_valid(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test2"

			data := []cluster.Data{
				{"bar", []byte("abcdefg"), 0},
				{"baz", []byte("qwertyuiop"), 0},
			}

			for _, entry := range data {
				publisher, closeConn :=
					test.NewPublisher(t, validSignFunc, testPrefix, entry.Source)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			for _, entry := range data {
				collector, closeConn :=
					test.NewCollector(t, validCheckFunc, testPrefix, entry.Source)
				defer closeConn()

				result, err := collector.Collect()
				require.NoError(t, err)

				require.Equal(t, result, []cluster.Data{
					{testPrefix + "/config/" + entry.Source, entry.Value, 0},
				})
			}
		})
	}
}

func TestIntegrityDataCollectorAllPublisherAll_valid(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test3"

			// Publish some data to check that "All" publisher erases it.
			data := []cluster.Data{
				{"bar", []byte("this shall be erased"), 0},
				{"baz", []byte("this shall be erased as well"), 0},
				{"", []byte("abcdefg"), 0},
			}

			for _, entry := range data {
				publisher, closeConn :=
					test.NewPublisher(t, validSignFunc, testPrefix, entry.Source)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			collector, closeConn := test.NewCollector(t, validCheckFunc, testPrefix, "")
			defer closeConn()

			result, err := collector.Collect()
			require.NoError(t, err)

			require.Equal(t, result, []cluster.Data{
				{testPrefix + "/config/all", data[2].Value, 0},
			})
		})
	}
}

func TestIntegrityDataCollectorKeyPublisherAll_valid(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test4"

			// Publish some data to check that "All" publisher erases it.
			data := []cluster.Data{
				{"bar", []byte("this shall be erased"), 0},
				{"baz", []byte("this shall be erased as well"), 0},
				{"", []byte("abcdefg"), 0},
			}

			for _, entry := range data {
				publisher, closeConn :=
					test.NewPublisher(t, validSignFunc, testPrefix, entry.Source)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			collector, closeConn := test.NewCollector(t, validCheckFunc, testPrefix, "all")
			defer closeConn()

			result, err := collector.Collect()
			require.NoError(t, err)

			require.Equal(t, result, []cluster.Data{
				{testPrefix + "/config/all", data[2].Value, 0},
			})
		})
	}
}

func TestIntegrityDataPublisher_CollectorAll_check_error(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test5"

			publisher, closeConn := test.NewPublisher(t, validSignFunc, testPrefix, "bar")
			defer closeConn()

			exampleData1 := []byte("abcdefg")
			err := publisher.Publish(0, exampleData1)
			require.NoError(t, err)

			collector, closeConn := test.NewCollector(t,
				func([]byte, map[string][]byte, []byte) error {
					return fmt.Errorf("any error")
				}, testPrefix, "")
			defer closeConn()

			result, err := collector.Collect()
			require.ErrorContains(t, err, "any error")
			require.Nil(t, result)
		})
	}
}

func TestIntegrityDataPublisher_CollectorKey_check_error(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test6"

			publisher, closeConn := test.NewPublisher(t, validSignFunc, testPrefix, "")
			defer closeConn()

			exampleData1 := []byte("abcdefg")
			err := publisher.Publish(0, exampleData1)
			require.NoError(t, err)

			collector, closeConn := test.NewCollector(t,
				func([]byte, map[string][]byte, []byte) error {
					return fmt.Errorf("any error")
				}, testPrefix, "all")
			defer closeConn()

			result, err := collector.Collect()
			require.ErrorContains(t, err, "any error")
			require.Nil(t, result)
		})
	}
}

func TestIntegrityDataPublisherKey_sign_error(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test7"

			publisher, closeConn := test.NewPublisher(t,
				func([]byte) (map[string][]byte, []byte, error) {
					return nil, nil, fmt.Errorf("any error")
				}, testPrefix, "bar")
			defer closeConn()

			exampleData1 := []byte("abcdefg")
			err := publisher.Publish(0, exampleData1)
			require.ErrorContains(t, err, "any error")
		})
	}
}

func TestIntegrityDataPublisherAll_sign_error(t *testing.T) {
	for _, test := range testsIntegrity {
		t.Run(test.Name, func(t *testing.T) {
			if !test.Applicable(t) {
				t.Skip()
			}

			inst := test.Setup(t)
			defer test.Shutdown(t, inst)

			const testPrefix = "/test8"

			publisher, closeConn := test.NewPublisher(t,
				func([]byte) (map[string][]byte, []byte, error) {
					return nil, nil, fmt.Errorf("any error")
				}, testPrefix, "")
			defer closeConn()

			exampleData1 := []byte("abcdefg")
			err := publisher.Publish(0, exampleData1)
			require.ErrorContains(t, err, "any error")
		})
	}
}
