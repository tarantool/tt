package cluster

import (
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/tt/cli/connector"
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
	NewTarantool(conn connector.Connector,
		prefix, key string, timeout time.Duration) (Collector, error)
}

// collectorsFactory is a type that implements a default CollectorFactory.
type collectorsFactory struct{}

// NewCollectorFactory creates a new CollectorFactory.
func NewCollectorFactory() CollectorFactory {
	return collectorsFactory{}
}

// NewFiler creates a new file configuration collector.
func (factory collectorsFactory) NewFile(path string) (Collector, error) {
	return NewFileCollector(path), nil
}

// NewEtcd creates a new etcd configuration collector.
func (factory collectorsFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (Collector, error) {
	if key == "" {
		return NewEtcdAllCollector(etcdcli, prefix, timeout), nil
	}
	return NewEtcdKeyCollector(etcdcli, prefix, key, timeout), nil
}

// NewTarantool creates creates a new tarantool config storage configuration
// collector.
func (factory collectorsFactory) NewTarantool(conn connector.Connector,
	prefix, key string, timeout time.Duration) (Collector, error) {
	if key == "" {
		return NewTarantoolAllCollector(conn, prefix, timeout), nil
	}
	return NewTarantoolKeyCollector(conn, prefix, key, timeout), nil
}
