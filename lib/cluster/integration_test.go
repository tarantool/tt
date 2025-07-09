//go:build integration

package cluster_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/tests/v3/integration"

	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/go-tarantool/v2/test_helpers"
	tcs_helper "github.com/tarantool/go-tarantool/v2/test_helpers/tcs"

	"github.com/tarantool/tt/lib/cluster"
)

const timeout = 5 * time.Second

func tcsIsSupported(t *testing.T) bool {
	ok, err := test_helpers.IsTcsSupported()
	if err != nil {
		t.Fatalf("Failed to check if TCS is supported: %s", err)
	}
	return ok
}

func startTcs(t *testing.T) *tcs_helper.TCS {
	tcs := tcs_helper.StartTesting(t, 3301)
	return &tcs
}

func stopTcs(t *testing.T, inst any) {
	tcs, ok := inst.(*tcs_helper.TCS)
	if !ok {
		t.Fatalf("Shutdown expected *tcs_helper.TCS, got %T", inst)
	}
	tcs.Stop()
}

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
	config := integration.ClusterConfig{Size: 1, PeerTLS: tls, UseTCP: true}
	inst := integration.NewLazyClusterWithConfig(config)

	if opts.Username != "" {
		etcd, err := cluster.ConnectEtcd(cluster.EtcdOpts{
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

type connectEtcdOpts struct {
	ServerOpts etcdOpts
	ClientOpts cluster.EtcdOpts
}

func TestConnectEtcd(t *testing.T) {
	// Client endpoints will be filled in the test runtime.
	cases := []struct {
		Name string
		Opts connectEtcdOpts
	}{
		{
			Name: "base",
			Opts: connectEtcdOpts{
				ServerOpts: etcdOpts{},
				ClientOpts: cluster.EtcdOpts{},
			},
		},
		{
			Name: "auth",
			Opts: connectEtcdOpts{
				ServerOpts: etcdOpts{
					Username: "root",
					Password: "pass",
				},
				ClientOpts: cluster.EtcdOpts{
					Username: "root",
					Password: "pass",
				},
			},
		},
		{
			Name: "tls_ca_file",
			Opts: connectEtcdOpts{
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
				},
				ClientOpts: cluster.EtcdOpts{
					CaFile: "testdata/tls/ca.crt",
				},
			},
		},
		{
			Name: "tls_ca_path",
			Opts: connectEtcdOpts{
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
				},
				ClientOpts: cluster.EtcdOpts{
					CaPath: "testdata/tls/",
				},
			},
		},
		{
			Name: "tls_ca_skip_verify",
			Opts: connectEtcdOpts{
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
				},
				ClientOpts: cluster.EtcdOpts{
					SkipHostVerify: true,
				},
			},
		},
		{
			Name: "tls_full",
			Opts: connectEtcdOpts{
				ServerOpts: etcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
					CaFile:   "testdata/tls/ca.crt",
				},
				ClientOpts: cluster.EtcdOpts{
					KeyFile:  "testdata/tls/localhost.key",
					CertFile: "testdata/tls/localhost.crt",
					CaFile:   "testdata/tls/ca.crt",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			inst := startEtcd(t, tc.Opts.ServerOpts)
			defer inst.Terminate()

			tc.Opts.ClientOpts.Endpoints = inst.EndpointsV3()
			etcd, err := cluster.ConnectEtcd(tc.Opts.ClientOpts)
			require.NoError(t, err)
			require.NotNil(t, etcd)
			defer etcd.Close()

			etcdPut(t, etcd, "foo", "bar")

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			gResp, err := etcd.Get(ctx, "foo")
			cancel()
			require.NoError(t, err)
			require.NotNil(t, gResp)
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
	inst := startEtcd(t, etcdOpts{})
	defer inst.Terminate()

	endpoints := inst.EndpointsV3()
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
		inst interface{},
	) (cluster.DataPublisher, func())
	NewCollector func(
		t *testing.T,
		checkFunc cluster.CheckFunc,
		prefix, key string,
		inst interface{},
	) (cluster.DataCollector, func())
}{
	{
		Name:       "tarantool",
		Applicable: tcsIsSupported,
		Setup: func(t *testing.T) interface{} {
			inst := startTcs(t)
			return inst
		},
		Shutdown: func(t *testing.T, inst interface{}) {
			stopTcs(t, inst)
		},
		NewPublisher: func(
			t *testing.T,
			signFunc cluster.SignFunc,
			prefix,
			key string,
			inst interface{},
		) (cluster.DataPublisher, func()) {
			tcs, ok := inst.(*tcs_helper.TCS)
			if !ok {
				t.Fatalf("NewPublisher expected *tcs_helper.TCS, got %T", inst)
			}

			publisherFactory := cluster.NewIntegrityDataPublisherFactory(signFunc)

			opts := tarantool.Opts{
				Timeout:       10 * time.Second,
				Reconnect:     10 * time.Second,
				MaxReconnects: 10,
			}

			conn, err := tarantool.Connect(context.Background(), tcs.Dialer(), opts)
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
			inst interface{},
		) (cluster.DataCollector, func()) {
			tcs, ok := inst.(*tcs_helper.TCS)
			if !ok {
				t.Fatalf("NewCollector expected *tcs_helper.TCS, got %T", inst)
			}
			collectorFactory := cluster.NewIntegrityDataCollectorFactory(checkFunc, nil)

			opts := tarantool.Opts{
				Timeout:       10 * time.Second,
				Reconnect:     10 * time.Second,
				MaxReconnects: 10,
			}

			conn, err := tarantool.Connect(context.Background(), tcs.Dialer(), opts)
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
			inst := startEtcd(t, etcdOpts{})
			return inst
		},
		Shutdown: func(t *testing.T, inst interface{}) {
			inst.(integration.LazyCluster).Terminate()
		},
		NewPublisher: func(
			t *testing.T,
			signFunc cluster.SignFunc,
			prefix,
			key string,
			inst interface{},
		) (cluster.DataPublisher, func()) {
			publisherFactory := cluster.NewIntegrityDataPublisherFactory(signFunc)
			etcdInst := inst.(integration.LazyCluster)

			etcd, err := clientv3.New(clientv3.Config{
				Endpoints:   etcdInst.EndpointsV3(),
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
			inst interface{},
		) (cluster.DataCollector, func()) {
			collectorFactory := cluster.NewIntegrityDataCollectorFactory(checkFunc, nil)
			etcdInst := inst.(integration.LazyCluster)

			etcd, err := clientv3.New(clientv3.Config{
				Endpoints:   etcdInst.EndpointsV3(),
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

// spell-checker:ignore abcdefg qwertyuiop

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
				publisher, closeConn := test.NewPublisher(
					t,
					validSignFunc,
					testPrefix,
					entry.Source,
					inst,
				)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			collector, closeConn := test.NewCollector(t, validCheckFunc, testPrefix, "", inst)
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
				publisher, closeConn := test.NewPublisher(
					t, validSignFunc, testPrefix, entry.Source, inst)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			for _, entry := range data {
				collector, closeConn := test.NewCollector(
					t, validCheckFunc, testPrefix, entry.Source, inst)
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
				publisher, closeConn := test.NewPublisher(
					t,
					validSignFunc,
					testPrefix,
					entry.Source,
					inst,
				)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			collector, closeConn := test.NewCollector(t, validCheckFunc, testPrefix, "", inst)
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
				publisher, closeConn := test.NewPublisher(
					t,
					validSignFunc,
					testPrefix,
					entry.Source,
					inst,
				)
				defer closeConn()

				err := publisher.Publish(entry.Revision, entry.Value)
				require.NoError(t, err)
			}

			collector, closeConn := test.NewCollector(t, validCheckFunc, testPrefix, "all", inst)
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

			publisher, closeConn := test.NewPublisher(t, validSignFunc, testPrefix, "bar", inst)
			defer closeConn()

			exampleData1 := []byte("abcdefg")
			err := publisher.Publish(0, exampleData1)
			require.NoError(t, err)

			collector, closeConn := test.NewCollector(t,
				func([]byte, map[string][]byte, []byte) error {
					return fmt.Errorf("any error")
				}, testPrefix, "", inst)
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

			publisher, closeConn := test.NewPublisher(t, validSignFunc, testPrefix, "", inst)
			defer closeConn()

			exampleData1 := []byte("abcdefg")
			err := publisher.Publish(0, exampleData1)
			require.NoError(t, err)

			collector, closeConn := test.NewCollector(t,
				func([]byte, map[string][]byte, []byte) error {
					return fmt.Errorf("any error")
				}, testPrefix, "all", inst)
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
				}, testPrefix, "bar", inst)
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
				}, testPrefix, "", inst)
			defer closeConn()

			exampleData1 := []byte("abcdefg")
			err := publisher.Publish(0, exampleData1)
			require.ErrorContains(t, err, "any error")
		})
	}
}
