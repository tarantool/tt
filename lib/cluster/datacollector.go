package cluster

import (
	"time"

	"github.com/tarantool/go-storage"
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
	// NewRemoteStorage creates a new data collector to collect configuration from
	// a remote storage.
	NewRemoteStorage(storage storage.Storage,
		prefix, key string, timeout time.Duration, storageType string) (DataCollector, error)
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

// NewRemoteStorage creates a new etcd configuration collector.
func (factory collectorsFactory) NewRemoteStorage(storage storage.Storage,
	prefix, key string, timeout time.Duration, storageType string,
) (DataCollector, error) {
	collector, err := NewConfigStorage(storage, prefix, timeout, key, storageType, factory.options.integrity)
	if err != nil {
		return nil, err
	}

	return collector, nil
}
