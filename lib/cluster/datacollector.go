package cluster

import (
	"time"

	"github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/driver/etcd"
	"github.com/tarantool/go-storage/driver/tcs"
	"github.com/tarantool/go-tarantool/v2"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// Data represents collected data with its source.
type Data struct {
	// Source is the origin of data, i.e. key in case of etcd or tarantool-based collectors.
	Source string
	// Value is data collected.
	Value []byte
	// Revision is data revision.
	Revision int64
}

// DataCollector interface must be implemented by a source collector.
type DataCollector interface {
	// Collect collects data from a source.
	Collect() ([]Data, error)
}

// DataCollectorFactory creates new data collectors.
type DataCollectorFactory interface {
	// NewFile creates a new data collector to collect configuration from a file.
	NewFile(path string) (DataCollector, error)
	// NewEtcd creates a new data collector to collect configuration from etcd.
	NewEtcd(etcdcli *clientv3.Client,
		prefix, key string, timeout time.Duration) (DataCollector, error)
	// NewTarantool creates a new data collector to collect configuration from
	// tarantool config storage.
	NewTarantool(conn tarantool.Doer,
		prefix, key string, timeout time.Duration) (DataCollector, error)
}

// collectorsFactory is a type that implements a default DataCollectorFactory.
type collectorsFactory struct {
	options factoryOptions
}

// NewDataCollectorFactory creates a new DataCollectorFactory.
func NewDataCollectorFactory(opts ...FactoryOption) DataCollectorFactory {
	return collectorsFactory{
		options: applyFactoryOptions(opts),
	}
}

// NewFiler creates a new file configuration collector.
func (factory collectorsFactory) NewFile(path string) (DataCollector, error) {
	return newFileCollector(path, factory.options.fileReadFunc), nil
}

// NewEtcd creates a new etcd configuration collector.
func (factory collectorsFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration,
) (DataCollector, error) {
	driver := etcd.New(etcdcli)
	storage := storage.NewStorage(driver)

	return NewStorage(storage, prefix, timeout, key, etcdStorageType, factory.options.integrity), nil
}

// NewTarantool creates creates a new tarantool config storage configuration
// collector.
func (factory collectorsFactory) NewTarantool(conn tarantool.Doer,
	prefix, key string, timeout time.Duration,
) (DataCollector, error) {
	driver := tcs.New(dummyDoerWatcher{conn})
	storage := storage.NewStorage(driver)

	return NewStorage(storage, prefix, timeout, key, tcsStorageType, factory.options.integrity), nil
}
