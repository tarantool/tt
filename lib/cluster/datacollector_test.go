package cluster_test

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool/v2"

	"github.com/tarantool/tt/lib/cluster"
)

func TestDataCollectorFactory(t *testing.T) {
	etcdcli := &clientv3.Client{}
	conn := &tarantool.Connection{}
	factory := cluster.NewDataCollectorFactory()

	noErr := func(collector cluster.DataCollector, err error) cluster.DataCollector {
		require.NoError(t, err)
		return collector
	}

	cases := []struct {
		Name      string
		Collector cluster.DataCollector
		Expected  cluster.DataCollector
	}{
		{
			Name:      "etcd_all",
			Collector: noErr(factory.NewEtcd(etcdcli, "foo", "", 1)),
			Expected:  cluster.NewEtcdAllCollector(etcdcli, "foo", 1),
		},
		{
			Name:      "etcd_key",
			Collector: noErr(factory.NewEtcd(etcdcli, "foo", "bar", 2)),
			Expected:  cluster.NewEtcdKeyCollector(etcdcli, "foo", "bar", 2),
		},
		{
			Name:      "tarantool_all",
			Collector: noErr(factory.NewTarantool(conn, "foo", "", 1)),
			Expected:  cluster.NewTarantoolAllCollector(conn, "foo", 1),
		},
		{
			Name:      "tarantool_key",
			Collector: noErr(factory.NewTarantool(conn, "foo", "bar", 2)),
			Expected:  cluster.NewTarantoolKeyCollector(conn, "foo", "bar", 2),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Collector)
		})
	}
}

func TestDataCollectorFactory_NewFile_not_exist(t *testing.T) {
	cases := []struct {
		Name    string
		Factory cluster.DataCollectorFactory
	}{
		{
			Name:    "base",
			Factory: cluster.NewDataCollectorFactory(),
		},
		{
			Name: "integrity",
			Factory: cluster.NewIntegrityDataCollectorFactory(nil,
				func(path string) (io.ReadCloser, error) {
					return os.Open(path)
				}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			collector, err := tc.Factory.NewFile("some/invalid/path")
			require.NoError(t, err)

			_, err = collector.Collect()
			assert.Error(t, err)
		})
	}
}

func TestDataCollectorFactory_NewFile_valid(t *testing.T) {
	expected := []cluster.Data{{
		Source: testYamlPath,
		Value: []byte(`config:
  version: 3.0.0
  hooks:
    post_cfg: /foo
    on_state_change: /bar
etcd:
  endpoints:
    - http://foo:4001
    - bar
  username: etcd
  password: not_a_secret
`),
	}}

	cases := []struct {
		Name    string
		Factory cluster.DataCollectorFactory
	}{
		{
			Name:    "base",
			Factory: cluster.NewDataCollectorFactory(),
		},
		{
			Name: "integrity",
			Factory: cluster.NewIntegrityDataCollectorFactory(nil,
				func(path string) (io.ReadCloser, error) {
					return os.Open(path)
				}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			collector, err := tc.Factory.NewFile(testYamlPath)
			require.NoError(t, err)

			data, err := collector.Collect()
			require.NoError(t, err)
			require.Equal(t, expected, data)
		})
	}
}
