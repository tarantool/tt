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
		var err error
		timeout := fmt.Sprintf("%fs", parsed.Http.Request.Timeout)
		opts.Timeout, err = time.ParseDuration(timeout)
		if err != nil {
			fmtErr := "unable to parse a etcd request timeout: %w"
			return EtcdOpts{}, fmt.Errorf(fmtErr, err)
		}
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
	prefix := strings.TrimRight(collector.prefix, "/")
	key := fmt.Sprintf("%s/%s/", prefix, "config")
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.getter.Get(ctx, key, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from etcd: %w", err)
	}

	cconfig := NewConfig()
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
