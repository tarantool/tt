package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/connector"
)

func TestCollectorFactory(t *testing.T) {
	etcdcli := &clientv3.Client{}
	conn := &connector.BinaryConnector{}
	factory := cluster.NewCollectorFactory()

	noErr := func(publisher cluster.Collector, err error) cluster.Collector {
		require.NoError(t, err)
		return publisher
	}

	cases := []struct {
		Name      string
		Collector cluster.Collector
		Expected  cluster.Collector
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
