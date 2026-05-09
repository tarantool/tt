package cluster

import (
	"time"

	"github.com/tarantool/go-storage"
)

// Collector interface must be implemented by a configuration source collector.
type Collector interface {
	// Collect collects a configuration or returns an error.
	Collect() (*Config, error)
}

// CollectorFactory creates new data collectors.
type CollectorFactory interface {
	// NewFile creates a new data collector to collect configuration from a file.
	NewFile(path string) (Collector, error)
	// NewRemoteStorage creates a new data collector to collect configuration from
	// a remote storage.
	NewRemoteStorage(storage storage.Storage,
		prefix, key string, timeout time.Duration, storageType string) (Collector, error)
}

// yamlDataCollectorFactoryDecorator is a wrapper over DataCollectorFactory turning
// it into a CollectorFactory.
type yamlDataCollectorFactoryDecorator struct {
	rawFactory DataCollectorFactory
}

// NewFile creates a new file configuration DataCollector and wraps it.
func (factory yamlDataCollectorFactoryDecorator) NewFile(path string) (Collector, error) {
	collector, err := factory.rawFactory.NewFile(path)
	return NewYamlCollectorDecorator(collector), err
}

// NewCollectorFactory turns arbitrary DataCollectorFactory into a
// CollectorFactory using YAML collector decorator.
func NewCollectorFactory(factory DataCollectorFactory) CollectorFactory {
	return yamlDataCollectorFactoryDecorator{
		rawFactory: factory,
	}
}

// NewRemoteStorage creates a new etcd configuration collector.
func (factory yamlDataCollectorFactoryDecorator) NewRemoteStorage(storage storage.Storage,
	prefix, key string, timeout time.Duration, storageType string,
) (Collector, error) {
	collector, err := factory.rawFactory.NewRemoteStorage(storage, prefix, key, timeout, storageType)
	return NewYamlCollectorDecorator(collector), err
}
