package cluster_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/lib/integrity"
)

func TestDataCollectorFactory(t *testing.T) {
	etcdcli := &clientv3.Client{}
	conn := &tarantool.Connection{}
	factory := cluster.NewDataCollectorFactory()

	noErr := func(collector integrity.DataCollector, err error) integrity.DataCollector {
		require.NoError(t, err)
		return collector
	}

	cases := []struct {
		Name      string
		Collector integrity.DataCollector
		Expected  integrity.DataCollector
	}{
		{
			Name:      "file",
			Collector: noErr(factory.NewFile("foo")),
			Expected:  cluster.NewFileCollector("foo"),
		},
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

type mockFileCollector struct {
	path string
}

func (mock mockFileCollector) Collect() ([]integrity.Data, error) {
	return nil, errors.New("not implemented")
}

type mockEtcdCollector struct {
	etcdcli *clientv3.Client
	prefix  string
	key     string
	timeout time.Duration
}

func (mock mockEtcdCollector) Collect() ([]integrity.Data, error) {
	return nil, errors.New("not implemented")
}

type mockTarantoolCollector struct {
	conn    tarantool.Connector
	prefix  string
	key     string
	timeout time.Duration
}

func (mock mockTarantoolCollector) Collect() ([]integrity.Data, error) {
	return nil, errors.New("not implemented")
}

type mockDataCollectorFactory struct{}

func (mock mockDataCollectorFactory) NewFile(path string) (integrity.DataCollector, error) {
	return mockFileCollector{
		path: path,
	}, nil
}

func (mock mockDataCollectorFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (integrity.DataCollector, error) {
	return mockEtcdCollector{
		etcdcli: etcdcli,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}, nil
}

func (mock mockDataCollectorFactory) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (integrity.DataCollector, error) {
	return mockTarantoolCollector{
		conn:    conn,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}, nil
}

func TestCollectorFactory(t *testing.T) {
	etcdcli := &clientv3.Client{}
	conn := &tarantool.Connection{}
	factory := cluster.NewCollectorFactory(mockDataCollectorFactory{})

	noErr := func(collector cluster.Collector, err error) cluster.Collector {
		require.NoError(t, err)
		return collector
	}

	cases := []struct {
		Name      string
		Collector cluster.Collector
		Expected  cluster.Collector
	}{
		{
			Name:      "file",
			Collector: noErr(factory.NewFile("foo")),
			Expected: cluster.NewYamlCollectorDecorator(mockFileCollector{
				path: "foo",
			}),
		},
		{
			Name:      "etcd_all",
			Collector: noErr(factory.NewEtcd(etcdcli, "foo", "", 1)),
			Expected: cluster.NewYamlCollectorDecorator(mockEtcdCollector{
				etcdcli: etcdcli,
				prefix:  "foo",
				key:     "",
				timeout: 1,
			}),
		},
		{
			Name:      "etcd_key",
			Collector: noErr(factory.NewEtcd(etcdcli, "foo", "bar", 2)),
			Expected: cluster.NewYamlCollectorDecorator(mockEtcdCollector{
				etcdcli: etcdcli,
				prefix:  "foo",
				key:     "bar",
				timeout: 2,
			}),
		},
		{
			Name:      "tarantool_all",
			Collector: noErr(factory.NewTarantool(conn, "foo", "", 1)),
			Expected: cluster.NewYamlCollectorDecorator(mockTarantoolCollector{
				conn:    conn,
				prefix:  "foo",
				key:     "",
				timeout: 1,
			}),
		},
		{
			Name:      "tarantool_key",
			Collector: noErr(factory.NewTarantool(conn, "foo", "bar", 2)),
			Expected: cluster.NewYamlCollectorDecorator(mockTarantoolCollector{
				conn:    conn,
				prefix:  "foo",
				key:     "bar",
				timeout: 2,
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Collector)
		})
	}
}
