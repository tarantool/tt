package cluster

import (
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/driver/etcd"
	"github.com/tarantool/go-storage/driver/tcs"
	"github.com/tarantool/go-tarantool/v2"
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
	NewTarantool(conn tarantool.Doer,
		prefix, key string, timeout time.Duration) (DataPublisher, error)
}

// publishersFactory is a type that implements a default DataPublisherFactory.
type publishersFactory struct {
	options factoryOptions
}

// NewDataPublisherFactory creates a new DataPublisherFactory.
func NewDataPublisherFactory(opts ...FactoryOption) DataPublisherFactory {
	return publishersFactory{
		options: applyFactoryOptions(opts),
	}
}

// NewFiler creates a new file data publisher.
func (factory publishersFactory) NewFile(path string) (DataPublisher, error) {
	if factory.options.integrity != nil {
		return nil, fmt.Errorf("publishing into a file with integrity data is not supported")
	}

	return NewFileDataPublisher(path), nil
}

// NewEtcd creates a new etcd data publisher.
func (factory publishersFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration,
) (DataPublisher, error) {
	driver := etcd.New(etcdcli)
	storage := storage.NewStorage(driver)

	return NewStorage(storage, prefix, timeout, key, etcdStorageType, factory.options.integrity), nil
}

// NewTarantool creates creates a new tarantool config storage data publisher.
func (factory publishersFactory) NewTarantool(conn tarantool.Doer,
	prefix, key string, timeout time.Duration,
) (DataPublisher, error) {
	driver := tcs.New(dummyDoerWatcher{conn})
	storage := storage.NewStorage(driver)

	return NewStorage(storage, prefix, timeout, key, tcsStorageType, factory.options.integrity), nil
}
