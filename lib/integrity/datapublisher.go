package integrity

import (
	"time"

	"github.com/tarantool/go-tarantool"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// DataPublisher interface must be implemented by a raw data publisher.
type DataPublisher interface {
	// Publish publishes the interface or returns an error.
	Publish(revision int64, data []byte) error
}

// Data publisher factory creates new data publishers.
type DataPublisherFactory interface {
	// NewFile creates a new DataPublisher to publish data into a file.
	NewFile(path string) (DataPublisher, error)
	// NewEtcd creates a new DataPublisher to publish data into etcd.
	NewEtcd(etcdcli *clientv3.Client,
		prefix, key string, timeout time.Duration) (DataPublisher, error)
	// NewTarantool creates a new DataPublisher to publish data into tarantool
	// config storage.
	NewTarantool(conn tarantool.Connector,
		prefix, key string, timeout time.Duration) (DataPublisher, error)
}
