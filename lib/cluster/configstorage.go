package cluster

import (
	"context"
	"fmt"

	libconnect "github.com/tarantool/tt/lib/connect"
)

type CSWatchEvent struct {
	Key   string
	Value []byte
}

// CSConnection interface is to be used to implement access to config storage.
type CSConnection interface {
	// Close closes connection.
	Close() error
	// Get retrieves value for key.
	Get(ctx context.Context, key string) ([]Data, error)
	// Put puts a key-value pair into config storage.
	Put(ctx context.Context, key, value string) error
	// Watch watches on a key and return watched events through the returned channel.
	Watch(ctx context.Context, key string) <-chan CSWatchEvent
}

// ConnectCStorage connects to config storage according to connection options.
func ConnectCStorage(
	uriOpts libconnect.UriOpts,
	connOpts ConnectOpts,
) (CSConnection, error) {
	sc, errEtcd := connectEtcdCS(uriOpts, connOpts)
	if errEtcd == nil {
		return sc, nil
	}

	sc, errTarantool := connectTarantoolCS(uriOpts, connOpts)
	if errTarantool == nil {
		return sc, nil
	}

	return nil, fmt.Errorf("failed to establish a connection to tarantool or etcd: %w, %w",
		errTarantool, errEtcd)
}
