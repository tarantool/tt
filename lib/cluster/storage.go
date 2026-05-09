package cluster

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/go-storage"
	gstorage "github.com/tarantool/go-storage"
	gsconnect "github.com/tarantool/go-storage/connect"
	"github.com/tarantool/go-storage/driver/etcd"
	"github.com/tarantool/go-storage/driver/tcs"
	"github.com/tarantool/go-storage/integrity"
	"github.com/tarantool/go-storage/marshaller"
	"github.com/tarantool/go-tarantool/v2"
	libconnect "github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/dial"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type StorageDataType = []byte

const (
	etcdStorageType = "etcd"
	tcsStorageType  = "tarantool"
	configLocation  = "config"
)

// IRawStorage is an interface that includes Collector, Publisher and CSConnection interfaces.
type IRawStorage interface {
	// Collect collects data from a source.
	Collect() ([]Data, error)
	// Publish publishes the interface or returns an error.
	Publish(revision int64, data []byte) error
	// Close closes connection.
	Close() error
	// Get retrieves value for key.
	Get(ctx context.Context, key string) ([]Data, error)
	// Put puts a key-value pair into config storage.
	Put(ctx context.Context, key, value string) error
	// Watch watches on a key and return watched events through the returned channel.
	Watch(ctx context.Context, key string) <-chan CSWatchEvent
}

// RawStorage implements IRawStorage.
type RawStorage struct {
	close          func() error
	storage        *integrity.Store[StorageDataType]
	codec          *integrity.Codec[StorageDataType]
	key            string
	storageType    string
	prefix         string
	objectLocation string
	timeout        time.Duration
}

// normalizeName normalizes a name by removing the prefix and object location from it.
// It trims leading slashes and strips the configured prefix and object location,
// returning the relative name within the storage.
func (r *RawStorage) normalizeName(name string) string {
	if name == "" {
		return ""
	}

	trimObjectLocation := func(name string) string {
		name = strings.TrimPrefix(name, "/")
		if r.objectLocation == "" {
			return name
		}
		if trimmed, ok := strings.CutPrefix(name, r.objectLocation); ok {
			return strings.TrimPrefix(trimmed, "/")
		}
		return name
	}

	if r.prefix == "" {
		return trimObjectLocation(name)
	}

	configPrefix := r.prefix
	if trimmed, ok := strings.CutPrefix(name, configPrefix); ok {
		return trimObjectLocation(trimmed)
	}

	configPrefix = strings.TrimSuffix(configPrefix, "/")
	if trimmed, ok := strings.CutPrefix(name, configPrefix); ok {
		return trimObjectLocation(trimmed)
	}

	return trimObjectLocation(name)
}

// sourceName constructs the full source path for a name by combining
// the prefix, object location and the normalized name.
func (r *RawStorage) sourceName(name string) string {
	name = r.normalizeName(name)
	if name == "" {
		return r.prefix
	}

	result := r.prefix
	if r.objectLocation != "" {
		result += "/" + r.objectLocation
	}
	return result + "/" + name
}

// collectByRange collects kvs by prefix.
func (r *RawStorage) collectByRange(ctx context.Context) ([]Data, error) {
	kvs, err := r.storage.Range(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from %s: %w", r.storageType, err)
	}

	if len(kvs) == 0 {
		return nil, fmt.Errorf("a configuration data not found in %s for prefix %q",
			r.storageType, r.prefix)
	}

	data := make([]Data, 0, len(kvs))
	for _, kv := range kvs {
		value, _ := kv.Value.Get()
		data = append(data, Data{
			Source:   r.sourceName(kv.Name),
			Value:    value,
			Revision: kv.ModRevision,
		})
	}

	slices.SortFunc(data, func(a, b Data) int {
		return cmp.Compare(a.Source, b.Source)
	})

	return data, nil
}

// collectByRange collects kvs by key.
func (r *RawStorage) collectByKey(ctx context.Context, key string) ([]Data, error) {
	resp, err := r.storage.Get(ctx, r.normalizeName(key))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from %s: %w", r.storageType, err)
	}

	data := make([]Data, 0, 1)

	value, _ := resp.Value.Get()
	data = append(data, Data{
		Source:   r.sourceName(resp.Name),
		Value:    value,
		Revision: resp.ModRevision,
	})

	return data, nil
}

// Collect collects values from storage by prefix or key.
func (r RawStorage) Collect() ([]Data, error) {
	ctx := context.Background()

	if r.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), r.timeout)
		defer cancel()
	}

	switch r.key {
	case "":
		return r.collectByRange(ctx)
	default:
		return r.collectByKey(ctx, r.key)
	}
}

// publishByKey put data by specific key.
func (r *RawStorage) publishByKey(ctx context.Context, key string, data []byte, revision int64) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, errDataMissing)
	}

	var predicates []integrity.Predicate
	if revision != 0 {
		predicates = append(predicates, r.codec.VersionEqual(revision))
	}

	err := r.storage.Put(ctx, r.normalizeName(key), data, integrity.WithPutPredicates(predicates...))
	if err != nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, err)
	}

	return nil
}

// publishByKey put data by prefix.
func (r *RawStorage) publishByRange(ctx context.Context, targetKey string, data []byte, revision int64) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, errDataMissing)
	}

	if revision != 0 {
		return fmt.Errorf("failed to publish data into %s: target revision %d is not supported",
			r.storageType, revision)
	}

	err := r.storage.Delete(ctx, "/", integrity.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to clean data from %s: %w", r.storageType, err)
	}

	err = r.storage.Put(ctx, r.normalizeName(targetKey), data)
	if err != nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, err)
	}

	return nil
}

// Publish publishes the configuration data into the storage.
// If the collector key is empty, it publishes to all keys with the prefix
// (deleting other keys under the prefix). Otherwise, it publishes to a
// specific key.
func (r RawStorage) Publish(revision int64, data []byte) error {
	ctx := context.Background()

	if r.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), r.timeout)
		defer cancel()
	}

	switch r.key {
	case "":
		return r.publishByRange(ctx, "all", data, revision)
	default:
		return r.publishByKey(ctx, r.key, data, revision)
	}
}

// Close closes a storage driver connection.
func (r *RawStorage) Close() error {
	return r.close()
}

// Get retrieves value for key.
func (r *RawStorage) Get(ctx context.Context, key string) ([]Data, error) {
	return r.collectByKey(ctx, key)
}

// Put puts a key-value pair into config storage.
func (r *RawStorage) Put(ctx context.Context, key, value string) error {
	return r.publishByKey(ctx, key, []byte(value), 0)
}

// Watch watches on a key and return watched events through the returned channel.
func (r *RawStorage) Watch(ctx context.Context, key string) (<-chan CSWatchEvent, error) {
	ch := make(chan CSWatchEvent)
	innerCh, err := r.storage.Watch(ctx, r.normalizeName(key))
	if err != nil {
		return nil, fmt.Errorf("failed to create watch channel: %w", err)
	}

	go func() {
		defer close(ch)

		for resp := range innerCh {
			value, _ := r.Get(ctx, string(resp.Prefix))
			ch <- CSWatchEvent{
				Key:   key,
				Value: value[0].Value,
			}
		}
	}()
	return ch, nil
}

// applyIntegrityBuilderOptions applies integrity options (hashers, signers,
// signers/verifiers) to the integrity codec builder.
func applyIntegrityBuilderOptions(
	builder integrity.CodecBuilder[StorageDataType],
	opts *IntegrityOptions,
) integrity.CodecBuilder[StorageDataType] {
	if opts == nil {
		return builder
	}

	for _, h := range opts.Hashers {
		builder = builder.WithHasher(h)
	}

	for _, sv := range opts.SignerVerifiers {
		builder = builder.WithSignerVerifier(sv)
	}

	for _, signer := range opts.Signers {
		builder = builder.WithSigner(signer)
	}

	for _, verifier := range opts.Verifiers {
		builder = builder.WithVerifier(verifier)
	}

	return builder
}

// NewStorage returns RawStorage with specified storageType.
func NewStorage(
	storage gstorage.Storage,
	prefix string,
	timeout time.Duration,
	key string,
	storageType string,
	integrityOpts *IntegrityOptions,
	objectLocation string,
) (*RawStorage, error) {
	prefix = strings.TrimRight(prefix, "/")

	codec := integrity.NewCodecBuilder[StorageDataType]().
		WithMarshaller(marshaller.NewTypedBytesMarshaller())

	if objectLocation != "" {
		codec = codec.WithObjectLocation(objectLocation)
	}

	codecBuild, err := applyIntegrityBuilderOptions(codec, integrityOpts).Build()
	if err != nil {
		return nil, err
	}

	storage, err = gstorage.Prefixed(prefix, storage)
	if err != nil {
		return nil, err
	}

	store := codecBuild.Bind(storage)

	return &RawStorage{
		storage:        store,
		codec:          codecBuild,
		key:            key,
		storageType:    storageType,
		timeout:        timeout,
		prefix:         prefix,
		objectLocation: objectLocation,
	}, nil
}

// NewConfigStorage returns RawStorage with config location.
func NewConfigStorage(
	storage gstorage.Storage,
	prefix string,
	timeout time.Duration,
	key string,
	storageType string,
	integrityOpts *IntegrityOptions,
) (*RawStorage, error) {
	return NewStorage(storage, prefix, timeout, key, storageType, integrityOpts, configLocation)
}

// connectEtcdClient creates and returns a new etcd client connection
// configured with the provided connection parameters, including TLS settings.
func connectEtcdClient(cfg gsconnect.Config) (*clientv3.Client, error) {
	var tlsConfig *tls.Config
	if cfg.SSL.KeyFile != "" || cfg.SSL.CertFile != "" || cfg.SSL.CaFile != "" ||
		cfg.SSL.CaPath != "" || !cfg.SSL.VerifyHost || !cfg.SSL.VerifyPeer {
		tlsInfo := transport.TLSInfo{
			CertFile:      cfg.SSL.CertFile,
			KeyFile:       cfg.SSL.KeyFile,
			TrustedCAFile: cfg.SSL.CaFile,
		}

		var err error
		tlsConfig, err = tlsInfo.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("fail to create tls client config: %w", err)
		}

		if cfg.SSL.CaPath != "" {
			roots, err := loadRootCA(cfg.SSL.CaPath)
			if err != nil {
				return nil, fmt.Errorf("fail to load CA directory: %w", err)
			}
			tlsConfig.RootCAs = roots
		}

		if !cfg.SSL.VerifyHost || !cfg.SSL.VerifyPeer {
			tlsConfig.InsecureSkipVerify = true
		}
	}

	return clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
		TLS:         tlsConfig,
		Logger:      zap.NewNop(),
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
	})
}

// connectTarantoolConnector creates and returns a new tarantool connection
// using the provided configuration for address, credentials and TLS.
func connectTarantoolConnector(cfg gsconnect.Config) (tarantool.Connector, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, fmt.Errorf("failed to connect to tarantool: at least one endpoint is required")
	}

	dialOpts := dial.Opts{
		Address:     cfg.Endpoints[0],
		User:        cfg.Username,
		Password:    cfg.Password,
		SslKeyFile:  cfg.SSL.KeyFile,
		SslCertFile: cfg.SSL.CertFile,
		SslCaFile:   cfg.SSL.CaFile,
		SslCiphers:  cfg.SSL.Ciphers,
	}

	dialer, err := dial.New(dialOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tarantool: %w", err)
	}

	connectorOpts := tarantool.Opts{
		Timeout: cfg.DialTimeout,
	}

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

// getEtcdCfg builds an etcd connection configuration from the given
// connect options and URI options, resolving credentials from options
// or environment variables as a fallback.
func getEtcdCfg(connOpts ConnectOpts, uriOpts libconnect.UriOpts) gsconnect.Config {
	var endpoints []string
	if uriOpts.Endpoint != "" {
		endpoints = []string{uriOpts.Endpoint}
	}

	if uriOpts.Username == "" && uriOpts.Password == "" {
		uriOpts.Username = connOpts.Username
		uriOpts.Password = connOpts.Password
		if uriOpts.Username == "" {
			uriOpts.Username = os.Getenv(libconnect.EtcdUsernameEnv)
		}
		if uriOpts.Password == "" {
			uriOpts.Password = os.Getenv(libconnect.EtcdPasswordEnv)
		}
	}

	return gsconnect.Config{
		Endpoints:   endpoints,
		Username:    uriOpts.Username,
		Password:    uriOpts.Password,
		DialTimeout: uriOpts.Timeout,
		SSL: gsconnect.SSLConfig{
			KeyFile:    uriOpts.KeyFile,
			CertFile:   uriOpts.CertFile,
			CaPath:     uriOpts.CaPath,
			CaFile:     uriOpts.CaFile,
			VerifyPeer: !uriOpts.SkipPeerVerify,
			VerifyHost: !uriOpts.SkipHostVerify,
		},
	}
}

// getTarantoolCfg builds a tarantool connection configuration from the given
// connect options and URI options, resolving credentials from options
// or environment variables as a fallback.
func getTarantoolCfg(connOpts ConnectOpts, uriOpts libconnect.UriOpts) gsconnect.Config {
	if uriOpts.Username == "" && uriOpts.Password == "" {
		uriOpts.Username = connOpts.Username
		uriOpts.Password = connOpts.Password
		if uriOpts.Username == "" {
			uriOpts.Username = os.Getenv(libconnect.TarantoolUsernameEnv)
		}
		if uriOpts.Password == "" {
			uriOpts.Password = os.Getenv(libconnect.TarantoolPasswordEnv)
		}
	}

	addr := fmt.Sprintf("tcp://%s", uriOpts.Host)

	return gsconnect.Config{
		Endpoints:   []string{addr},
		Username:    uriOpts.Username,
		Password:    uriOpts.Password,
		DialTimeout: uriOpts.Timeout,
		SSL: gsconnect.SSLConfig{
			KeyFile:  uriOpts.KeyFile,
			CertFile: uriOpts.CertFile,
			CaFile:   uriOpts.CaFile,
			Ciphers:  uriOpts.Ciphers,
		},
	}
}

// NewStorageConnection determines a storage based on the opts.
func NewStorageConnection(connOpts ConnectOpts, opts libconnect.UriOpts) (storage.Storage, gsconnect.CleanupFunc, string, error) {
	etcdCfg := getEtcdCfg(connOpts, opts)
	etcdClient, errEtcd := connectEtcdClient(etcdCfg)
	if errEtcd == nil {
		driver := etcd.New(etcdClient)
		return storage.NewStorage(driver), func() { _ = etcdClient.Close() }, etcdStorageType, nil
	}

	tcsCfg := getTarantoolCfg(connOpts, opts)
	conn, errTCS := connectTarantoolConnector(tcsCfg)
	if errTCS == nil {
		driver := tcs.New(conn)
		return storage.NewStorage(driver), func() { _ = conn.Close() }, tcsStorageType, nil
	}

	return nil, func() {}, "", fmt.Errorf("failed to establish a connection to tarantool or etcd: %w, %w",
		errTCS, errEtcd)
}

// loadRootCA and the code below is a copy-paste from Go sources. We need an
// ability to load certificates from a directory, but there is no such public
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

// ConnectOpts is additional connect options specified by a user.
type ConnectOpts struct {
	Username string
	Password string
}
