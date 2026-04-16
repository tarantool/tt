package cmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/tarantool/go-storage"
	etcdDriver "github.com/tarantool/go-storage/driver/etcd"
	tcsDriver "github.com/tarantool/go-storage/driver/tcs"
	"github.com/tarantool/go-tarantool/v2"
	libconnect "github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/dial"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// ConnectStorage connects to etcd or tarantool config storage and returns
// a storage.Storage instance. It tries etcd first, then tarantool.
func ConnectStorage(
	opts libconnect.UriOpts,
	username, password string,
) (storage.Storage, func(), error) {
	etcdcli, errEtcd := connectEtcd(opts, username, password)
	if errEtcd == nil {
		drv := etcdDriver.New(etcdcli)
		stg := storage.NewStorage(drv)
		return stg, func() { etcdcli.Close() }, nil
	}

	conn, errTarantool := connectTarantool(opts, username, password)
	if errTarantool == nil {
		drv := tcsDriver.New(conn)
		stg := storage.NewStorage(drv)
		return stg, func() { conn.Close() }, nil
	}

	return nil, nil, fmt.Errorf("failed to connect to etcd or tarantool: %w, %w",
		errTarantool, errEtcd)
}

// connectEtcd creates an etcd client from URI options.
func connectEtcd(
	opts libconnect.UriOpts,
	username, password string,
) (*clientv3.Client, error) {
	var endpoints []string
	if opts.Endpoint != "" {
		endpoints = []string{opts.Endpoint}
	}

	etcdUsername := username
	etcdPassword := password
	if etcdUsername == "" {
		etcdUsername = os.Getenv(libconnect.EtcdUsernameEnv)
	}
	if etcdPassword == "" {
		etcdPassword = os.Getenv(libconnect.EtcdPasswordEnv)
	}

	var tlsConfig *tls.Config
	if opts.KeyFile != "" || opts.CertFile != "" || opts.CaFile != "" ||
		opts.CaPath != "" || opts.SkipHostVerify {

		tlsInfo := transport.TLSInfo{
			CertFile:      opts.CertFile,
			KeyFile:       opts.KeyFile,
			TrustedCAFile: opts.CaFile,
		}

		var err error
		tlsConfig, err = tlsInfo.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create tls client config: %w", err)
		}

		if opts.CaPath != "" {
			roots, err := loadRootCACerts(opts.CaPath)
			if err != nil {
				return nil, fmt.Errorf("failed to load CA directory: %w", err)
			}
			tlsConfig.RootCAs = roots
		}

		if opts.SkipHostVerify {
			tlsConfig.InsecureSkipVerify = true
		}
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: opts.Timeout,
		Username:    etcdUsername,
		Password:    etcdPassword,
		TLS:         tlsConfig,
		Logger:      zap.NewNop(),
		DialOptions: []grpc.DialOption{grpc.WithBlock()}, //nolint:staticcheck
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	return client, nil
}

// connectTarantool creates a tarantool connection from URI options.
func connectTarantool(
	opts libconnect.UriOpts,
	username, password string,
) (*tarantool.Connection, error) {
	tarantoolUsername := username
	tarantoolPassword := password
	if tarantoolUsername == "" {
		tarantoolUsername = os.Getenv(libconnect.TarantoolUsernameEnv)
	}
	if tarantoolPassword == "" {
		tarantoolPassword = os.Getenv(libconnect.TarantoolPasswordEnv)
	}

	dialOpts := dial.Opts{
		Address:     fmt.Sprintf("tcp://%s", opts.Host),
		User:        tarantoolUsername,
		Password:    tarantoolPassword,
		SslKeyFile:  opts.KeyFile,
		SslCertFile: opts.CertFile,
		SslCaFile:   opts.CaFile,
		SslCiphers:  opts.Ciphers,
	}

	dialer, err := dial.New(dialOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create dialer: %w", err)
	}

	connectorOpts := tarantool.Opts{
		Timeout: opts.Timeout,
	}

	ctx, cancel := contextWithTimeout(connectorOpts.Timeout)
	defer cancel()

	conn, err := tarantool.Connect(ctx, dialer, connectorOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tarantool: %w", err)
	}

	return conn, nil
}

// contextWithTimeout creates a context with optional timeout.
func contextWithTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(context.Background(), timeout)
	}
	return context.Background(), func() {}
}

// loadRootCACerts loads root CA certificates from a directory.
func loadRootCACerts(caPath string) (*x509.CertPool, error) {
	roots := x509.NewCertPool()

	files, err := os.ReadDir(caPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA directory: %w", err)
	}

	for _, f := range files {
		if f.IsDir() || isSameDirSymlink(f, caPath) {
			continue
		}

		data, err := os.ReadFile(caPath + "/" + f.Name())
		if err != nil {
			continue
		}

		roots.AppendCertsFromPEM(data)
	}

	return roots, nil
}

// isSameDirSymlink checks if a directory entry is a symlink pointing to the same directory.
func isSameDirSymlink(f fs.DirEntry, dir string) bool {
	if f.Type()&fs.ModeSymlink == 0 {
		return false
	}

	target, err := os.Readlink(dir + "/" + f.Name())
	return err == nil && !strings.Contains(target, "/")
}
