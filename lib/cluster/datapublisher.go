package cluster

import (
	"fmt"
	"time"

	"github.com/tarantool/go-storage"
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
	// NewRemoteStorage creates a new data collector to collect configuration from
	// a remote storage.
	NewRemoteStorage(storage storage.Storage,
		prefix, key string, timeout time.Duration, storageType string) (DataPublisher, error)
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

// NewRemoteStorage creates a new etcd configuration collector.
func (factory publishersFactory) NewRemoteStorage(storage storage.Storage,
	prefix, key string, timeout time.Duration, storageType string,
) (DataPublisher, error) {
	collector, err := NewConfigStorage(storage, prefix, timeout, key, storageType, factory.options.integrity)
	if err != nil {
		return nil, err
	}

	return collector, nil
}
