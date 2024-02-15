package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/lib/integrity"
)

func TestDataPublisherFactory(t *testing.T) {
	etcdcli := &clientv3.Client{}
	conn := &tarantool.Connection{}
	factory := cluster.NewDataPublisherFactory()

	noErr := func(publisher integrity.DataPublisher, err error) integrity.DataPublisher {
		require.NoError(t, err)
		return publisher
	}

	cases := []struct {
		Name      string
		Publisher integrity.DataPublisher
		Expected  integrity.DataPublisher
	}{
		{
			Name:      "file",
			Publisher: noErr(factory.NewFile("foo")),
			Expected:  cluster.NewFileDataPublisher("foo"),
		},
		{
			Name:      "etcd_all",
			Publisher: noErr(factory.NewEtcd(etcdcli, "foo", "", 1)),
			Expected:  cluster.NewEtcdAllDataPublisher(etcdcli, "foo", 1),
		},
		{
			Name:      "etcd_key",
			Publisher: noErr(factory.NewEtcd(etcdcli, "foo", "bar", 2)),
			Expected:  cluster.NewEtcdKeyDataPublisher(etcdcli, "foo", "bar", 2),
		},
		{
			Name:      "tarantool_all",
			Publisher: noErr(factory.NewTarantool(conn, "foo", "", 1)),
			Expected:  cluster.NewTarantoolAllDataPublisher(conn, "foo", 1),
		},
		{
			Name:      "tarantool_key",
			Publisher: noErr(factory.NewTarantool(conn, "foo", "bar", 2)),
			Expected:  cluster.NewTarantoolKeyDataPublisher(conn, "foo", "bar", 2),
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Equal(t, tc.Expected, tc.Publisher)
		})
	}
}
