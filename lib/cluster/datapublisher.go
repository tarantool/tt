package cluster

import (
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"
)

// DataPublisher interface must be implemented by a raw data publisher.
type DataPublisher interface {
	// Publish publishes the interface or returns an error.
	Publish(revision int64, data []byte) error
}

// DataPublisherFactory creates new data publishers.
type DataPublisherFactory interface {
	// NewFile creates a new data publisher to publish data into a file.
	NewFile(path string) (DataPublisher, error)
	// NewEtcd creates a new data publisher to publish data into etcd.
	NewEtcd(etcdcli *clientv3.Client,
		prefix, key string, timeout time.Duration) (DataPublisher, error)
	// NewTarantool creates a new data publisher to publish data into tarantool
	// config storage.
	NewTarantool(conn tarantool.Connector,
		prefix, key string, timeout time.Duration) (DataPublisher, error)
}

// publishersFactory is a type that implements a default DataPublisherFactory.
type publishersFactory struct{}

// NewDataPublisherFactory creates a new DataPublisherFactory.
func NewDataPublisherFactory() DataPublisherFactory {
	return publishersFactory{}
}

// NewFiler creates a new file data publisher.
func (factory publishersFactory) NewFile(path string) (DataPublisher, error) {
	return NewFileDataPublisher(path), nil
}

// NewEtcd creates a new etcd data publisher.
func (factory publishersFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (DataPublisher, error) {
	if key == "" {
		return NewEtcdAllDataPublisher(etcdcli, prefix, timeout), nil
	}
	return NewEtcdKeyDataPublisher(etcdcli, prefix, key, timeout), nil
}

// NewTarantool creates creates a new tarantool config storage data publisher.
func (factory publishersFactory) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (DataPublisher, error) {
	if key == "" {
		return NewTarantoolAllDataPublisher(conn, prefix, timeout), nil
	}
	return NewTarantoolKeyDataPublisher(conn, prefix, key, timeout), nil
}

// integrityPublishersFactory is a type that implements a default
// DataPublisherFactory.
type integrityPublishersFactory struct {
	signFunc SignFunc
}

// NewIntegrityDataPublisherFactory creates a new DataPublisherFactory with
// integrity signing.
func NewIntegrityDataPublisherFactory(signFunc SignFunc) DataPublisherFactory {
	return integrityPublishersFactory{
		signFunc: signFunc,
	}
}

// NewFiler creates a new file data publisher with integrity signing.
func (factory integrityPublishersFactory) NewFile(path string) (DataPublisher, error) {
	return nil, fmt.Errorf("publishing into a file with integrity data is not supported")
}

// NewEtcd creates a new etcd data publisher with integrity signing.
func (factory integrityPublishersFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (DataPublisher, error) {
	if key == "" {
		return NewIntegrityEtcdAllDataPublisher(factory.signFunc, etcdcli, prefix, timeout), nil
	}
	return NewIntegrityEtcdKeyDataPublisher(factory.signFunc, etcdcli, prefix, key, timeout), nil
}

// NewTarantool creates creates a new tarantool config storage data publisher
// with integrity signing.
func (factory integrityPublishersFactory) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (DataPublisher, error) {
	if key == "" {
		return NewIntegrityTarantoolAllDataPublisher(factory.signFunc,
			conn, prefix, timeout), nil
	}
	return NewIntegrityTarantoolKeyDataPublisher(factory.signFunc,
		conn, prefix, key, timeout), nil
}
