package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"

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
