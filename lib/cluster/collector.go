package cluster

import (
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool/v2"
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
	// NewEtcd creates a new data collector to collect configuration from etcd.
	NewEtcd(etcdcli *clientv3.Client,
		prefix, key string, timeout time.Duration) (Collector, error)
	// NewTarantool creates a new data collector to collect configuration from
	// tarantool config storage.
	NewTarantool(conn tarantool.Connector,
		prefix, key string, timeout time.Duration) (Collector, error)
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

// NewEtcd creates a new etcd DataCollector and wraps it.
func (factory yamlDataCollectorFactoryDecorator) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (Collector, error) {
	collector, err := factory.rawFactory.NewEtcd(etcdcli, prefix, key, timeout)
	return NewYamlCollectorDecorator(collector), err
}

// NewTarantool creates a new tarantool DataCollector and wraps it.
func (factory yamlDataCollectorFactoryDecorator) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (Collector, error) {
	collector, err := factory.rawFactory.NewTarantool(conn, prefix, key, timeout)
	return NewYamlCollectorDecorator(collector), err
}

// NewCollectorFactory turns arbitrary DataCollectorFactory into a
// CollectorFactory using YAML collector decorator.
func NewCollectorFactory(factory DataCollectorFactory) CollectorFactory {
	return yamlDataCollectorFactoryDecorator{
		rawFactory: factory,
	}
}
