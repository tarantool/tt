package cluster

import (
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/cli/integrity"
)

// DataPublisherFactory creates new data publishers.
type DataPublisherFactory interface {
	// NewFile creates a new data publisher to publish data into a file.
	NewFile(path string) (integrity.DataPublisher, error)
	// NewEtcd creates a new data publisher to publish data into etcd.
	NewEtcd(etcdcli *clientv3.Client,
		prefix, key string, timeout time.Duration) (integrity.DataPublisher, error)
	// NewTarantool creates a new data publisher to publish data into tarantool
	// config storage.
	NewTarantool(conn tarantool.Connector,
		prefix, key string, timeout time.Duration) (integrity.DataPublisher, error)
}

// publishersFactory is a type that implements a default DataPublisherFactory.
type publishersFactory struct{}

// NewDataPublisherFactory creates a new DataPublisherFactory.
func NewDataPublisherFactory() integrity.DataPublisherFactory {
	return publishersFactory{}
}

// NewFiler creates a new file data publisher.
func (factory publishersFactory) NewFile(path string) (integrity.DataPublisher, error) {
	return NewFileDataPublisher(path), nil
}

// NewEtcd creates a new etcd data publisher.
func (factory publishersFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (integrity.DataPublisher, error) {
	if key == "" {
		return NewEtcdAllDataPublisher(etcdcli, prefix, timeout), nil
	}
	return NewEtcdKeyDataPublisher(etcdcli, prefix, key, timeout), nil
}

// NewTarantool creates creates a new tarantool config storage data publisher.
func (factory publishersFactory) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (integrity.DataPublisher, error) {
	if key == "" {
		return NewTarantoolAllDataPublisher(conn, prefix, timeout), nil
	}
	return NewTarantoolKeyDataPublisher(conn, prefix, key, timeout), nil
}
