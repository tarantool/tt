package cluster

import (
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/lib/integrity"
)

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

// collectorsFactory is a type that implements a default CollectorFactory.
type collectorsFactory struct{}

// NewDataCollectorFactory creates a new CollectorFactory.
func NewDataCollectorFactory() integrity.DataCollectorFactory {
	return collectorsFactory{}
}

// NewFiler creates a new file configuration collector.
func (factory collectorsFactory) NewFile(path string) (integrity.DataCollector, error) {
	return NewFileCollector(path), nil
}

// NewEtcd creates a new etcd configuration collector.
func (factory collectorsFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (integrity.DataCollector, error) {
	if key == "" {
		return NewEtcdAllCollector(etcdcli, prefix, timeout), nil
	}
	return NewEtcdKeyCollector(etcdcli, prefix, key, timeout), nil
}

// NewTarantool creates creates a new tarantool config storage configuration
// collector.
func (factory collectorsFactory) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (integrity.DataCollector, error) {
	if key == "" {
		return NewTarantoolAllCollector(conn, prefix, timeout), nil
	}
	return NewTarantoolKeyCollector(conn, prefix, key, timeout), nil
}

// yamlDataCollectorFactoryDecorator is a wrapper over DataCollectorFactory turning
// it into a CollectorFactory.
type yamlDataCollectorFactoryDecorator struct {
	rawFactory integrity.DataCollectorFactory
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
func NewCollectorFactory(factory integrity.DataCollectorFactory) CollectorFactory {
	return yamlDataCollectorFactoryDecorator{
		rawFactory: factory,
	}
}
