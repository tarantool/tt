package connect

import (
	"context"
	"fmt"
	"os"

	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/go-tlsdialer"
	libcluster "github.com/tarantool/tt/lib/cluster"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// ConnectOpts is additional connect options specified by a user.
type ConnectOpts struct {
	Username string
	Password string
}

// connectEtcd establishes a connection to etcd.
func connectEtcd(uriOpts UriOpts, connOpts ConnectOpts) (*clientv3.Client, error) {
	etcdOpts := MakeEtcdOptsFromUriOpts(uriOpts)
	if etcdOpts.Username == "" && etcdOpts.Password == "" {
		etcdOpts.Username = connOpts.Username
		etcdOpts.Password = connOpts.Password
		if etcdOpts.Username == "" {
			etcdOpts.Username = os.Getenv(EtcdUsernameEnv)
		}
		if etcdOpts.Password == "" {
			etcdOpts.Password = os.Getenv(EtcdPasswordEnv)
		}
	}

	etcdcli, err := libcluster.ConnectEtcd(etcdOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}
	return etcdcli, nil
}

// connectTarantool establishes a connection to Tarantool.
func connectTarantool(uriOpts UriOpts,
	connOpts ConnectOpts) (tarantool.Connector, error) {
	if uriOpts.Username == "" && uriOpts.Password == "" {
		uriOpts.Username = connOpts.Username
		uriOpts.Password = connOpts.Password
		if uriOpts.Username == "" {
			uriOpts.Username = os.Getenv(TarantoolUsernameEnv)
		}
		if uriOpts.Password == "" {
			uriOpts.Password = os.Getenv(TarantoolPasswordEnv)
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
func doOnStorage(connOpts ConnectOpts, opts UriOpts,
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

// CreateCollector creates a new data publisher and collector based on UriOpts.
func CreateCollector(
	collectors libcluster.CollectorFactory,
	connOpts ConnectOpts,
	opts UriOpts) (libcluster.Collector, func(), error) {
	prefix, key, timeout := opts.Prefix, opts.Params["key"], opts.Timeout

	var (
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
		return nil, nil, err
	}

	return collector, closeFunc, nil
}

// MakeEtcdOptsFromUriOpts create etcd connect options from URI options.
func MakeEtcdOptsFromUriOpts(src UriOpts) libcluster.EtcdOpts {
	var endpoints []string
	if src.Endpoint != "" {
		endpoints = []string{src.Endpoint}
	}

	return libcluster.EtcdOpts{
		Endpoints:      endpoints,
		Username:       src.Username,
		Password:       src.Password,
		KeyFile:        src.KeyFile,
		CertFile:       src.CertFile,
		CaPath:         src.CaPath,
		CaFile:         src.CaFile,
		SkipHostVerify: src.SkipHostVerify || src.SkipPeerVerify,
		Timeout:        src.Timeout,
	}
}

// MakeConnectOptsFromUriOpts create Tarantool connect options from
// URI options.
func MakeConnectOptsFromUriOpts(src UriOpts) (tarantool.Dialer, tarantool.Opts) {
	address := fmt.Sprintf("tcp://%s", src.Host)

	var dialer tarantool.Dialer

	if src.KeyFile != "" || src.CertFile != "" || src.CaFile != "" || src.Ciphers != "" {
		dialer = tlsdialer.OpenSSLDialer{
			Address:     address,
			User:        src.Username,
			Password:    src.Password,
			SslKeyFile:  src.KeyFile,
			SslCertFile: src.CertFile,
			SslCaFile:   src.CaFile,
			SslCiphers:  src.Ciphers,
		}
	} else {
		dialer = tarantool.NetDialer{
			Address:  address,
			User:     src.Username,
			Password: src.Password,
		}
	}

	opts := tarantool.Opts{
		Timeout: src.Timeout,
	}

	return dialer, opts
}
