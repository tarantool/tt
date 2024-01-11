package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarantool/tt/cli/integrity"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// EtcdOpts is a way to configure a etcd client.
type EtcdOpts struct {
	// Endpoints a slice of endpoints to connect.
	Endpoints []string
	// Username is a user name for authorization
	Username string
	// Password is a password for authorization
	Password string
	// KeyFile is a path to a private SSL key file.
	KeyFile string
	// CertFile is a path to an SSL certificate file.
	CertFile string
	// CaPath is a path to a trusted certificate authorities (CA) directory.
	CaPath string
	// CaFile is a path to a trusted certificate authorities (CA) file.
	CaFile string
	// SkipHostVerify controls whether a client verifies the server's
	// certificate chain and host name. This is dangerous option so by
	// default it is false.
	SkipHostVerify bool
	// Timeout is a timeout for actions.
	Timeout time.Duration
}

// ConnectEtcd creates a new client object for a etcd from the specified
// options.
func ConnectEtcd(opts EtcdOpts) (*clientv3.Client, error) {
	var tlsConfig *tls.Config = nil
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
			return nil, fmt.Errorf("fail to create tls client config: %w", err)
		}

		if opts.CaPath != "" {
			var err error
			tlsConfig.RootCAs, err = loadRootCA(opts.CaPath)
			if err != nil {
				return nil, fmt.Errorf("fail to load CA directory: %w", err)
			}
		}

		if opts.SkipHostVerify {
			tlsConfig.InsecureSkipVerify = true
		}
	}

	return clientv3.New(clientv3.Config{
		Endpoints:   opts.Endpoints,
		DialTimeout: opts.Timeout,
		Username:    opts.Username,
		Password:    opts.Password,
		TLS:         tlsConfig,
		Logger:      zap.NewNop(),
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
	})
}

// EtcdGetter is the interface that wraps get from etcd method.
type EtcdGetter interface {
	// Get retrieves key-value pairs for a key.
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

// EtcdAllCollector collects data from a etcd connection for a whole prefix.
type EtcdAllCollector struct {
	getter  EtcdGetter
	prefix  string
	timeout time.Duration
}

// NewEtcdAllCollector creates a new collector for etcd from the whole prefix.
func NewEtcdAllCollector(getter EtcdGetter, prefix string, timeout time.Duration) EtcdAllCollector {
	return EtcdAllCollector{
		getter:  getter,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified prefix with the
// specified timeout.
func (collector EtcdAllCollector) Collect() ([]integrity.Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.getter.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from etcd: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("a configuration data not found in etcd for prefix %q",
			prefix)
	}

	collected := []integrity.Data{}
	for _, kv := range resp.Kvs {
		collected = append(collected, integrity.Data{
			Source: string(kv.Key),
			Value:  kv.Value,
		})
	}

	return collected, nil
}

// EtcdKeyCollector collects data from a etcd connection for a whole prefix.
type EtcdKeyCollector struct {
	getter  EtcdGetter
	prefix  string
	key     string
	timeout time.Duration
}

// NewEtcdKeyCollector creates a new collector for etcd from a key from
// a prefix.
func NewEtcdKeyCollector(getter EtcdGetter, prefix, key string,
	timeout time.Duration) EtcdKeyCollector {
	return EtcdKeyCollector{
		getter:  getter,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified path with the specified
// timeout.
func (collector EtcdKeyCollector) Collect() ([]integrity.Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	key := prefix + collector.key
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.getter.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from etcd: %w", err)
	}

	switch {
	case len(resp.Kvs) == 0:
		// It should not happen, but we need to be sure to avoid a null pointer
		// dereference.
		return nil, fmt.Errorf("a configuration data not found in etcd for key %q",
			key)
	case len(resp.Kvs) > 1:
		return nil, fmt.Errorf("too many responses (%v) from etcd for key %q",
			resp.Kvs, key)
	}

	collected := []integrity.Data{
		{
			Source: string(resp.Kvs[0].Key),
			Value:  resp.Kvs[0].Value,
		},
	}
	return collected, nil
}

// EtcdTxnGetter is the interface that adds Txn method to EtcdGetter.
type EtcdTxnGetter interface {
	EtcdGetter
	// Txn creates a transaction.
	Txn(ctx context.Context) clientv3.Txn
}

// EtcdAllDataPublisher publishes a data into etcd to a prefix.
type EtcdAllDataPublisher struct {
	getter  EtcdTxnGetter
	prefix  string
	timeout time.Duration
}

// NewEtcdAllDataPublisher creates a new EtcdAllDataPublisher object to publish
// a data to etcd with the prefix during the timeout.
func NewEtcdAllDataPublisher(getter EtcdTxnGetter,
	prefix string, timeout time.Duration) EtcdAllDataPublisher {
	return EtcdAllDataPublisher{
		getter:  getter,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Publish publishes the configuration into etcd to the given prefix.
func (publisher EtcdAllDataPublisher) Publish(data []byte) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into etcd: data does not exist")
	}

	prefix := getConfigPrefix(publisher.prefix)
	key := prefix + "all"
	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	for true {
		// The code tries to put data with the key and remove all other
		// data with the prefix. We need to remove all other data with the
		// prefix because actually the cluster config could be split
		// into several parts with the same prefix.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// First of all we need to get all paths with the prefix.
		resp, err := publisher.getter.Get(ctx, prefix, clientv3.WithPrefix())
		if err != nil {
			return fmt.Errorf("failed to fetch data from etcd: %w", err)
		}

		// Then we need to delete all other paths and put the configuration
		// into the target key. We do it in a single transaction to avoid
		// concurrent updates and collisions.

		// Fill the delete part of the transaction.
		var (
			keys      []string
			revisions []int64
		)
		for _, kv := range resp.Kvs {
			// We need to skip the target key since etcd transactions do not
			// support delete + put for the same key on a single transaction.
			if string(kv.Key) != key {
				keys = append(keys, string(kv.Key))
				revisions = append(revisions, kv.ModRevision)
			}
		}

		var (
			cmps []clientv3.Cmp
			opts []clientv3.Op
		)
		for i, key := range keys {
			cmp := clientv3.Compare(clientv3.ModRevision(key), "=", revisions[i])
			cmps = append(cmps, cmp)
			opts = append(opts, clientv3.OpDelete(key))
		}

		// Fill the put part of the transaction.
		opts = append(opts, clientv3.OpPut(key, string(data)))
		txn := publisher.getter.Txn(ctx)
		if len(cmps) > 0 {
			txn = txn.If(cmps...)
		}

		// And try to execute the transaction.
		tresp, err := txn.Then(opts...).Commit()

		if err != nil {
			return fmt.Errorf("failed to put data into etcd: %w", err)
		}
		if tresp != nil && tresp.Succeeded {
			return nil
		}
	}
	// Unreachable.
	return nil
}

// EtcdPutter is the interface that wraps put from etcd method.
type EtcdPutter interface {
	// Put puts a key-value pair into etcd.
	Put(ctx context.Context, key, val string,
		opts ...clientv3.OpOption) (*clientv3.PutResponse, error)
}

// EtcdKeyDataPublisher publishes a data into etcd for a prefix and a key.
type EtcdKeyDataPublisher struct {
	putter  EtcdPutter
	prefix  string
	key     string
	timeout time.Duration
}

// NewEtcdKeyDataPublisher creates a new EtcdKeyDataPublisher object to publish
// a data to etcd with the prefix and key during the timeout.
func NewEtcdKeyDataPublisher(putter EtcdPutter,
	prefix, key string, timeout time.Duration) EtcdKeyDataPublisher {
	return EtcdKeyDataPublisher{
		putter:  putter,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Publish publishes the configuration into etcd to the given prefix and key.
func (publisher EtcdKeyDataPublisher) Publish(data []byte) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into etcd: data does not exist")
	}

	prefix := getConfigPrefix(publisher.prefix)
	key := prefix + publisher.key
	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	_, err := publisher.putter.Put(ctx, key, string(data))
	if err != nil {
		return fmt.Errorf("failed to put data into etcd: %w", err)
	}

	return nil
}

// loadRootCA and the code below is a copy-paste from Go sources. We need an
// ability to load ceritificates from a directory, but there is no such public
// function in `crypto`.
//
// https://cs.opensource.google/go/go/+/refs/tags/go1.20.7:src/crypto/x509/root_unix.go;l=62-74
func loadRootCA(path string) (*x509.CertPool, error) {
	roots := x509.NewCertPool()

	fis, err := readUniqueDirectoryEntries(path)
	if err != nil {
		return nil, err
	}

	rootsLen := 0
	for _, fi := range fis {
		data, err := os.ReadFile(path + "/" + fi.Name())
		if err == nil {
			rootsLen++
			roots.AppendCertsFromPEM(data)
		}
	}

	return roots, nil
}

// readUniqueDirectoryEntries is like os.ReadDir but omits
// symlinks that point within the directory.
//
// https://cs.opensource.google/go/go/+/refs/tags/go1.20.7:src/crypto/x509/root_unix.go;l=84-98
func readUniqueDirectoryEntries(dir string) ([]fs.DirEntry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	uniq := files[:0]
	for _, f := range files {
		if !isSameDirSymlink(f, dir) {
			uniq = append(uniq, f)
		}
	}

	return uniq, nil
}

// isSameDirSymlink reports whether fi in dir is a symlink with a
// target not containing a slash.
//
// https://cs.opensource.google/go/go/+/refs/tags/go1.20.7:src/crypto/x509/root_unix.go;l=100-108
func isSameDirSymlink(f fs.DirEntry, dir string) bool {
	if f.Type()&fs.ModeSymlink == 0 {
		return false
	}

	target, err := os.Readlink(filepath.Join(dir, f.Name()))
	return err == nil && !strings.Contains(target, "/")
}

// getConfigPrefix returns a full configuration prefix.
func getConfigPrefix(basePrefix string) string {
	prefix := strings.TrimRight(basePrefix, "/")
	return fmt.Sprintf("%s/%s/", prefix, "config")
}
