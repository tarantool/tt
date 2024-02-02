package integrity

import (
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
