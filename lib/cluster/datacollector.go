package cluster

import (
	"fmt"
	"time"

	"github.com/tarantool/go-tarantool"
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
	NewTarantool(conn tarantool.Connector,
		prefix, key string, timeout time.Duration) (DataCollector, error)
}

// collectorsFactory is a type that implements a default DataCollectorFactory.
type collectorsFactory struct{}

// NewDataCollectorFactory creates a new DataCollectorFactory.
func NewDataCollectorFactory() DataCollectorFactory {
	return collectorsFactory{}
}

// NewFiler creates a new file configuration collector.
func (factory collectorsFactory) NewFile(path string) (DataCollector, error) {
	return NewFileCollector(path), nil
}

// NewEtcd creates a new etcd configuration collector.
func (factory collectorsFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (DataCollector, error) {
	if key == "" {
		return NewEtcdAllCollector(etcdcli, prefix, timeout), nil
	}
	return NewEtcdKeyCollector(etcdcli, prefix, key, timeout), nil
}

// NewTarantool creates creates a new tarantool config storage configuration
// collector.
func (factory collectorsFactory) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (DataCollector, error) {
	if key == "" {
		return NewTarantoolAllCollector(conn, prefix, timeout), nil
	}
	return NewTarantoolKeyCollector(conn, prefix, key, timeout), nil
}

// integrityCollectorsFactory is a type that implements a default CollectorFactory.
type integrityCollectorsFactory struct {
	checkFunc    CheckFunc
	fileReadFunc FileReadFunc
}

// NewIntegrityDataCollectorFactory creates a new DataCollectorFactory with
// integrity checks.
func NewIntegrityDataCollectorFactory(checkFunc CheckFunc,
	fileReadFunc FileReadFunc) DataCollectorFactory {
	return integrityCollectorsFactory{
		checkFunc:    checkFunc,
		fileReadFunc: fileReadFunc,
	}
}

// NewFiler creates a new file configuration collector with integrity checks.
func (factory integrityCollectorsFactory) NewFile(path string) (DataCollector, error) {
	return NewIntegrityFileCollector(factory.fileReadFunc, path), nil
}

// NewEtcd creates a new etcd configuration collector with integrity checks.
func (factory integrityCollectorsFactory) NewEtcd(etcdcli *clientv3.Client,
	prefix, key string, timeout time.Duration) (DataCollector, error) {
	return nil, fmt.Errorf("unimplemented")
}

// NewTarantool creates creates a new tarantool config storage configuration
// collector with integrity checks.
func (factory integrityCollectorsFactory) NewTarantool(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) (DataCollector, error) {
	return nil, fmt.Errorf("unimplemented")
}
