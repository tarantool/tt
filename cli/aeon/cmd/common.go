package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/tarantool/go-tarantool/v2"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// connectEtcd establishes a connection to etcd.
func connectEtcd(uriOpts UriOpts, connOpts connectOpts) (*clientv3.Client, error) {
	etcdOpts := MakeEtcdOptsFromUriOpts(uriOpts)
	if etcdOpts.Username == "" && etcdOpts.Password == "" {
		etcdOpts.Username = connOpts.Username
		etcdOpts.Password = connOpts.Password
		if etcdOpts.Username == "" {
			etcdOpts.Username = os.Getenv(connect.EtcdUsernameEnv)
		}
		if etcdOpts.Password == "" {
			etcdOpts.Password = os.Getenv(connect.EtcdPasswordEnv)
		}
	}

	etcdcli, err := libcluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	return etcdcli, nil
}

// connectTarantool establishes a connection to Tarantool.
func connectTarantool(uriOpts UriOpts, connOpts connectOpts) (tarantool.Connector, error) {
	if uriOpts.Username == "" && uriOpts.Password == "" {
		uriOpts.Username = connOpts.Username
		uriOpts.Password = connOpts.Password
		if uriOpts.Username == "" {
			uriOpts.Username = os.Getenv(connect.TarantoolUsernameEnv)
		}
		if uriOpts.Password == "" {
			uriOpts.Password = os.Getenv(connect.TarantoolPasswordEnv)
		}
	}

	dialer, connectorOpts := MakeConnectOptsFromUriOpts(uriOpts)

	ctx := context.Background()
	if connectorOpts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, connectorOpts.Timeout)
		defer cancel()
	}
	conn, err := tarantool.Connect(ctx, dialer, connectorOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tarantool: %w", err)
	}
	return conn, nil
}

// doOnStorage determines a storage based on the opts.
func doOnStorage(connOpts connectOpts, opts UriOpts,
	tarantoolFunc func(tarantool.Connector) error, etcdFunc func(*clientv3.Client) error) error {
	etcdcli, errEtcd := connectEtcd(opts, connOpts)
	if errEtcd == nil {
		return etcdFunc(etcdcli)
	}

	conn, errTarantool := connectTarantool(opts, connOpts)
	if errTarantool == nil {
		return tarantoolFunc(conn)
	}

	return fmt.Errorf("failed to establish a connection to tarantool or etcd: %w, %w",
		errTarantool, errEtcd)
}

// createPublisherAndCollector creates a new data publisher and collector based on UriOpts.
func createPublisherAndCollector(
	publishers libcluster.DataPublisherFactory,
	collectors libcluster.CollectorFactory,
	connOpts connectOpts,
	opts UriOpts) (libcluster.DataPublisher, libcluster.Collector, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Key, opts.Timeout

	var (
		publisher libcluster.DataPublisher
		collector libcluster.Collector
		err       error
		closeFunc func()
	)

	tarantoolFunc := func(conn tarantool.Connector) error {
		if collectors != nil {
			collector, err = collectors.NewTarantool(conn, prefix, key, timeout)
			if err != nil {
				conn.Close()
				return fmt.Errorf("failed to create tarantool config storage collector: %w", err)
			}
		}
		closeFunc = func() { conn.Close() }
		return nil
	}

	etcdFunc := func(client *clientv3.Client) error {
		if publishers != nil {
			publisher, err = publishers.NewEtcd(client, prefix, key, timeout)
			if err != nil {
				client.Close()
				return fmt.Errorf("failed to create etcd publisher: %w", err)
			}
		}
		if collectors != nil {
			collector, err = collectors.NewEtcd(client, prefix, key, timeout)
			if err != nil {
				client.Close()
				return fmt.Errorf("failed to create etcd collector: %w", err)
			}
		}
		closeFunc = func() { client.Close() }
		return nil
	}

	if err := doOnStorage(connOpts, opts, tarantoolFunc, etcdFunc); err != nil {
		return nil, nil, nil, err
	}

	return publisher, collector, closeFunc, nil
}
