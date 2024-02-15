package cluster_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/lib/cluster"
)

func TestDataPublisherFactory(t *testing.T) {
	etcdcli := &clientv3.Client{}
	conn := &tarantool.Connection{}
	factory := cluster.NewDataPublisherFactory()

	noErr := func(publisher cluster.DataPublisher, err error) cluster.DataPublisher {
		require.NoError(t, err)
		return publisher
	}

	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
		Expected  cluster.DataPublisher
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

func TestIntegrityDataPublisherFactory_NewFile(t *testing.T) {
	factory := cluster.NewIntegrityDataPublisherFactory(nil)
	publisher, err := factory.NewFile("any")

	assert.Nil(t, publisher)
	assert.EqualError(t, err,
		"publishing into a file with integrity data is not supported")
}
