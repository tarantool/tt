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

	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"gopkg.in/yaml.v2"
)

// EtcdOpts is a way to configure a etcd client.
type EtcdOpts struct {
	// Endpoints a slice of endpoints to connect.
	Endpoints []string
	// Prefix is a configuration prefix.
	Prefix string
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

// MakeEtcdOpts creates a EtcdOpts object filled from a configuration.
func MakeEtcdOpts(config *Config) (EtcdOpts, error) {
	etcdConfig := NewConfig()
	etcdPath := []string{"config", "etcd"}
	config.ForEach(etcdPath, func(path []string, value any) {
		path = path[len(etcdPath):]
		etcdConfig.Set(path, value)
	})

	type parsedEtcdConfig struct {
		Endpoints []string `yaml:"endpoints"`
		Username  string   `yaml:"username"`
		Password  string   `yaml:"password"`
		Prefix    string   `yaml:"prefix"`
		Ssl       struct {
			KeyFile    string `yaml:"ssl_key"`
			CertFile   string `yaml:"cert_file"`
			CaPath     string `yaml:"ca_path"`
			CaFile     string `yaml:"ca_file"`
			VerifyPeer bool   `yaml:"verify_peer"`
			VerifyHost bool   `yaml:"verify_host"`
		} `yaml:"ssl"`
		Http struct {
			Request struct {
				Timeout float64 `yaml:"timeout"`
			} `yaml:"request"`
		} `yaml:"http"`
	}
	var parsed parsedEtcdConfig
	parsed.Ssl.VerifyPeer = true
	parsed.Ssl.VerifyHost = true

	if err := yaml.Unmarshal([]byte(etcdConfig.String()), &parsed); err != nil {
		fmtErr := "unable to parse etcd config: %w"
		return EtcdOpts{}, fmt.Errorf(fmtErr, err)
	}
	opts := EtcdOpts{
		Endpoints: parsed.Endpoints,
		Prefix:    parsed.Prefix,
		Username:  parsed.Username,
		Password:  parsed.Password,
		KeyFile:   parsed.Ssl.KeyFile,
		CertFile:  parsed.Ssl.CertFile,
		CaPath:    parsed.Ssl.CaPath,
		CaFile:    parsed.Ssl.CaFile,
	}
	if !parsed.Ssl.VerifyPeer || !parsed.Ssl.VerifyHost {
		opts.SkipHostVerify = true
	}
	if parsed.Http.Request.Timeout != 0 {
		timeoutFloat := parsed.Http.Request.Timeout * float64(time.Second)
		opts.Timeout = time.Duration(timeoutFloat)
	}

	return opts, nil
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
	})
}

// EtcdCollector collects data from a etcd connection.
type EtcdCollector struct {
	getter  EtcdGetter
	prefix  string
	timeout time.Duration
}

// EtcdGetter is the interface that wraps get from etcd method.
type EtcdGetter interface {
	// Get retrieves key-value pairs for a key.
	Get(ctx context.Context, key string, opts ...clientv3.OpOption) (*clientv3.GetResponse, error)
}

// NewEtcdCollector creates a new collector for etcd from the path.
func NewEtcdCollector(getter EtcdGetter, prefix string, timeout time.Duration) EtcdCollector {
	return EtcdCollector{
		getter:  getter,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified path with the specified
// timeout.
func (collector EtcdCollector) Collect() (*Config, error) {
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

	cconfig := NewConfig()
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("a configuration data not found in prefix %q",
			prefix)
	}

	for _, kv := range resp.Kvs {
		if config, err := NewYamlCollector(kv.Value).Collect(); err != nil {
			fmtErr := "failed to decode etcd config for key %q: %w"
			return nil, fmt.Errorf(fmtErr, string(kv.Key), err)
		} else {
			cconfig.Merge(config)
		}
	}

	return cconfig, nil
}

// EtcdDataPublisher publishes a data into etcd.
type EtcdDataPublisher struct {
	getter  EtcdTxnGetter
	prefix  string
	timeout time.Duration
}

// EtcdUpdater is the interface that adds Txn method to EtcdGetter.
type EtcdTxnGetter interface {
	EtcdGetter
	// Txn creates a transaction.
	Txn(ctx context.Context) clientv3.Txn
}

// NewEtcdDataPublisher creates a new EtcdDataPublisher object to publish
// a data to etcd with the prefix during the timeout.
func NewEtcdDataPublisher(getter EtcdTxnGetter,
	prefix string, timeout time.Duration) EtcdDataPublisher {
	return EtcdDataPublisher{
		getter:  getter,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Publish publishes the configuration into etcd to the given prefix.
func (publisher EtcdDataPublisher) Publish(data []byte) error {
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
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := publisher.getter.Get(ctx, prefix, clientv3.WithPrefix())
		if err != nil {
			return fmt.Errorf("failed to fetch data from etcd: %w", err)
		}

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

		opts = append(opts, clientv3.OpPut(key, string(data)))
		txn := publisher.getter.Txn(ctx)
		if len(cmps) > 0 {
			txn = txn.If(cmps...)
		}
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
