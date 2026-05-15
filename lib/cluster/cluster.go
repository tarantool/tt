package cluster

import (
	"fmt"
	"time"

	libconnect "github.com/tarantool/tt/lib/connect"
)

const (
	defaultEtcdTimeout = 3 * time.Second
)

// CreateDataCollector creates a DataCollector for the storage described by
// connOpts and opts (etcd or Tarantool config storage). It is the
// DataCollector-level analogue of the former CreateCollector.
func CreateDataCollector(
	factory DataCollectorFactory,
	connOpts ConnectOpts,
	opts libconnect.UriOpts,
) (DataCollector, func(), error) {
	stor, cleanup, storageType, err := NewStorageConnection(connOpts, opts)
	if err != nil {
		return nil, nil, err
	}

	collector, err := factory.NewRemoteStorage(stor, opts.Prefix, opts.Params["key"], opts.Timeout, storageType)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create %s collector: %w", storageType, err)
	}

	return collector, func() { cleanup() }, nil
}
