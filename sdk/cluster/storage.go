package cluster

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	gstorage "github.com/tarantool/go-storage/v2"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
	"github.com/tarantool/go-storage/v2/driver/etcd"
	"github.com/tarantool/go-storage/v2/integrity"
	"github.com/tarantool/go-storage/v2/marshaller"
	"github.com/tarantool/go-storage/v2/namer"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// ErrNoConfiguration reports a storage that holds no configuration under the
// prefix a collector reads. The error that wraps it names the storage and
// the prefix.
var ErrNoConfiguration = errors.New("a configuration data not found in ")

// ErrIntegrityCheckFailed reports data in a storage that fails the integrity
// checks: its hash or signature is missing, does not match the data or was
// made with another key. The error that wraps it names the storage, the key
// and the reason.
var ErrIntegrityCheckFailed = errors.New("integrity check failed")

var (
	errFailedToConnectToTarantoolAtLeastOneEndpointIsRequired = errors.New(
		"failed to connect to tarantool: at least one endpoint is required",
	)
	errTargetRevisionIsNotSupported = errors.New("target revision ")
)

// WatchEvent is delivered on Watch channels when a watched key changes.
type WatchEvent struct {
	Key   string
	Value []byte
}

type StorageDataType = []byte

const (
	etcdStorageType = "etcd"
	tcsStorageType  = "tarantool"
	configLocation  = "config"
)

// RawStorage is a connection-bound view of a remote configuration storage
// (etcd or tarantool config storage). It implements both DataCollector and
// DataPublisher as well as direct Get/Put/Watch/Close operations on individual
// keys.
type RawStorage struct {
	storage        *integrity.Store[StorageDataType]
	codec          *integrity.Codec[StorageDataType]
	cleanup        func()
	key            string
	storageType    string
	prefix         string
	objectLocation string
	timeout        time.Duration
}

// NewStorage returns a *RawStorage bound to the given backend.
//
// Most callers should use Factory.NewRemoteStorage, which fills objectLocation
// with the default configLocation. Use this directly only when a different
// objectLocation (or none) is required, such as for the failover command
// namespace.
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
		WithMarshaller(marshaller.NewBytesMarshaller()).
		WithNamer(newLegacyNamer)
	if objectLocation != "" {
		codec = codec.WithObjectLocation(objectLocation)
	}

	if integrityOpts != nil {
		// The names of the signatures, a signer and its verifier sharing one.
		sigs := make(map[string]struct{})

		for _, h := range integrityOpts.Hashers {
			codec = codec.WithHasher(h)
		}

		for _, sv := range integrityOpts.SignerVerifiers {
			codec = codec.WithSignerVerifier(sv)
			sigs[sv.Name()] = struct{}{}
		}

		for _, signer := range integrityOpts.Signers {
			codec = codec.WithSigner(signer)
			sigs[signer.Name()] = struct{}{}
		}

		for _, verifier := range integrityOpts.Verifiers {
			codec = codec.WithVerifier(verifier)
			sigs[verifier.Name()] = struct{}{}
		}

		// The key of the one signature names no algorithm, as in tt 2.
		if len(sigs) == 1 {
			codec = codec.WithSingleSigCompact()
		}
	}

	codecBuild, err := codec.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build the integrity codec: %w", err)
	}

	storage, err = gstorage.Prefixed(prefix, storage)
	if err != nil {
		return nil, fmt.Errorf("failed to scope the storage to prefix %q: %w", prefix, err)
	}

	return &RawStorage{
		storage:        codecBuild.Bind(storage),
		codec:          codecBuild,
		cleanup:        nil,
		key:            key,
		storageType:    storageType,
		timeout:        timeout,
		prefix:         prefix,
		objectLocation: objectLocation,
	}, nil
}

// newLegacyNamer lays the keys of a configuration out as tt 2 and Tarantool
// Enterprise Edition do:
//
//	<prefix>/<objectLocation>/<name>      the value
//	<prefix>/hashes/<hasher>/<name>       a hash, one per hasher
//	<prefix>/sig/<name>                   the signature
//
// that is, <prefix>/config/all with <prefix>/hashes/sha256/all and
// <prefix>/sig/all for a configuration published whole. The hash and the
// signature keys do not repeat the object location (namer.LegacyHashSigLayout);
// that the signature key names no algorithm when there is one signature is
// the codec's WithSingleSigCompact, which NewStorage sets.
func newLegacyNamer(
	objectLocation string,
	hashLocations []namer.HashLocation,
	sigLocations []namer.SigLocation,
	opts ...namer.Option,
) (namer.Namer, error) {
	opts = append(opts, namer.LegacyHashSigLayout())

	layout, err := namer.New(objectLocation, hashLocations, sigLocations, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to lay the keys out: %w", err)
	}

	return layout, nil
}

// SetCleanup attaches a cleanup callback that Close will invoke. Used when the
// caller wants Close to also release an underlying connection.
func (r *RawStorage) SetCleanup(cleanup func()) {
	r.cleanup = cleanup
}

// Collect collects values from storage by prefix (if no key bound) or by key.
func (r *RawStorage) Collect() ([]Data, error) {
	ctx, cancel := r.withTimeout()
	defer cancel()

	if r.key != "" {
		return r.Get(ctx, r.key)
	}

	// Range drops a value that fails the checks unless told otherwise, which
	// would report tampered data as no data at all. Every value is taken and
	// a failed check is reported for its key instead.
	kvs, err := r.storage.Range(ctx, "", integrity.IgnoreVerificationError())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from %s: %w", r.storageType, err)
	}

	for _, keyValue := range kvs {
		if keyValue.Error != nil {
			return nil, r.integrityError(keyValue.Name, keyValue.Error)
		}
	}

	if len(kvs) == 0 {
		return nil, fmt.Errorf("%w%s for prefix %q",
			ErrNoConfiguration, r.storageType, r.prefix)
	}

	data := make([]Data, 0, len(kvs))
	for _, keyValue := range kvs {
		value, _ := keyValue.Value.Get()

		data = append(data, Data{
			Source:   r.sourceName(keyValue.Name),
			Value:    value,
			Revision: keyValue.ModRevision,
		})
	}

	slices.SortFunc(data, func(a, b Data) int {
		return cmp.Compare(a.Source, b.Source)
	})

	return data, nil
}

// Publish publishes the configuration data into the storage.
// If the collector key is empty, it publishes to all keys with the prefix
// (deleting other keys under the prefix). Otherwise, it publishes to a
// specific key.
func (r *RawStorage) Publish(revision int64, data []byte) error {
	ctx, cancel := r.withTimeout()
	defer cancel()

	if data == nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, errDataMissing)
	}

	if r.key != "" {
		return r.put(ctx, r.key, data, revision)
	}

	if revision != 0 {
		return fmt.Errorf("failed to publish data into %s: %w%d is not supported",
			r.storageType, errTargetRevisionIsNotSupported, revision)
	}

	err := r.storage.Delete(ctx, "/", integrity.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to clean data from %s: %w", r.storageType, err)
	}

	err = r.storage.Put(ctx, r.normalizeName("all"), data)
	if err != nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, err)
	}

	return nil
}

// Close releases the underlying storage connection (if SetCleanup was called).
func (r *RawStorage) Close() error {
	if r.cleanup != nil {
		r.cleanup()
	}

	return nil
}

// Get retrieves the value for key.
func (r *RawStorage) Get(ctx context.Context, key string) ([]Data, error) {
	resp, err := r.storage.Get(ctx, r.normalizeName(key), integrity.IgnoreVerificationError())
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from %s: %w", r.storageType, err)
	}

	if resp.Error != nil {
		return nil, r.integrityError(resp.Name, resp.Error)
	}

	value, _ := resp.Value.Get()

	return []Data{{
		Source:   r.sourceName(resp.Name),
		Value:    value,
		Revision: resp.ModRevision,
	}}, nil
}

// Put puts a key-value pair into config storage.
func (r *RawStorage) Put(ctx context.Context, key, value string) error {
	return r.put(ctx, key, []byte(value), 0)
}

// Watch watches on a key and return watched events through the returned channel.
func (r *RawStorage) Watch(ctx context.Context, key string) (<-chan WatchEvent, error) {
	events := make(chan WatchEvent)

	innerCh, err := r.storage.Watch(ctx, r.normalizeName(key))
	if err != nil {
		return nil, fmt.Errorf("failed to create watch channel: %w", err)
	}

	go func() {
		defer close(events)

		for resp := range innerCh {
			// A value that cannot be read, or fails the integrity checks, is
			// no event: the watcher waits for the next change.
			value, err := r.Get(ctx, string(resp.Key))
			if err != nil || len(value) == 0 {
				continue
			}

			events <- WatchEvent{
				Key:   key,
				Value: value[0].Value,
			}
		}
	}()

	return events, nil
}

// integrityError reports the value under name that failed the integrity
// checks with err.
func (r *RawStorage) integrityError(name string, err error) error {
	return fmt.Errorf("%w for the data of %s key %q: %w",
		ErrIntegrityCheckFailed, r.storageType, r.sourceName(name), err)
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

// withTimeout returns a context bounded by r.timeout (background context if 0).
func (r *RawStorage) withTimeout() (context.Context, context.CancelFunc) {
	if r.timeout == 0 {
		return context.Background(), func() {}
	}

	return context.WithTimeout(context.Background(), r.timeout)
}

func (r *RawStorage) put(ctx context.Context, key string, data []byte, revision int64) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, errDataMissing)
	}

	var predicates []integrity.Predicate

	if revision != 0 {
		predicates = append(predicates, r.codec.VersionEqual(revision))
	}

	err := r.storage.Put(ctx, r.normalizeName(key), data,
		integrity.WithPutPredicates(predicates...))
	if err != nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, err)
	}

	return nil
}

// connectEtcdClient creates and returns a new etcd client connection
// configured with the provided connection parameters, including TLS settings.
func connectEtcdClient(cfg gsconnect.Config) (*clientv3.Client, error) {
	var tlsConfig *tls.Config

	if needsEtcdTLSConfig(cfg.SSL) {
		var err error

		tlsConfig, err = makeEtcdTLSConfig(cfg.SSL)
		if err != nil {
			return nil, err
		}
	}

	client, err := clientv3.New(clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
		TLS:         tlsConfig,
		Logger:      zap.NewNop(),
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	return client, nil
}

func needsEtcdTLSConfig(sslCfg gsconnect.SSLConfig) bool {
	return sslCfg.Enable || sslCfg.KeyFile != "" || sslCfg.CertFile != "" || sslCfg.CaFile != "" ||
		sslCfg.CaPath != "" || !sslCfg.VerifyHost || !sslCfg.VerifyPeer
}

func makeEtcdTLSConfig(sslCfg gsconnect.SSLConfig) (*tls.Config, error) {
	tlsInfo := transport.TLSInfo{
		CertFile:      sslCfg.CertFile,
		KeyFile:       sslCfg.KeyFile,
		TrustedCAFile: sslCfg.CaFile,
	}

	tlsConfig, err := tlsInfo.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("fail to create tls client config: %w", err)
	}

	if sslCfg.CaPath != "" {
		roots, err := loadRootCA(sslCfg.CaPath)
		if err != nil {
			return nil, fmt.Errorf("fail to load CA directory: %w", err)
		}

		tlsConfig.RootCAs = roots
	}

	// HTTPS without a custom CA encrypts the connection without verifying
	// the server certificate, preserving cluster URI semantics. A configured CA
	// still enables verification unless it is explicitly disabled.
	if !sslCfg.VerifyHost || !sslCfg.VerifyPeer ||
		(sslCfg.Enable && sslCfg.CaFile == "" && sslCfg.CaPath == "") {
		tlsConfig.InsecureSkipVerify = true
	}

	return tlsConfig, nil
}

// getEtcdCfg builds an etcd connection configuration from the given
// connect options and URI options, resolving credentials from options
// or environment variables as a fallback.
func getEtcdCfg(connOpts ConnectOpts, uriOpts sdkconnect.URIOpts) gsconnect.Config {
	var endpoints []string

	if uriOpts.Endpoint != "" {
		endpoints = []string{uriOpts.Endpoint}
	}

	if uriOpts.Username == "" && uriOpts.Password == "" {
		uriOpts.Username = connOpts.Username
		uriOpts.Password = connOpts.Password

		if uriOpts.Username == "" {
			uriOpts.Username = os.Getenv(sdkconnect.EtcdUsernameEnv)
		}

		if uriOpts.Password == "" {
			uriOpts.Password = os.Getenv(sdkconnect.EtcdPasswordEnv)
		}
	}

	return gsconnect.Config{
		Endpoints:   endpoints,
		Username:    uriOpts.Username,
		Password:    uriOpts.Password,
		DialTimeout: uriOpts.Timeout,
		SSL: gsconnect.SSLConfig{
			Enable:     uriOpts.Scheme == "https",
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
func getTarantoolCfg(connOpts ConnectOpts, uriOpts sdkconnect.URIOpts) gsconnect.Config {
	if uriOpts.Username == "" && uriOpts.Password == "" {
		uriOpts.Username = connOpts.Username
		uriOpts.Password = connOpts.Password

		if uriOpts.Username == "" {
			uriOpts.Username = os.Getenv(sdkconnect.TarantoolUsernameEnv)
		}

		if uriOpts.Password == "" {
			uriOpts.Password = os.Getenv(sdkconnect.TarantoolPasswordEnv)
		}
	}

	addr := "tcp://" + uriOpts.Host

	return gsconnect.Config{
		Endpoints:   []string{addr},
		Username:    uriOpts.Username,
		Password:    uriOpts.Password,
		DialTimeout: uriOpts.Timeout,
		SSL: gsconnect.SSLConfig{
			Enable: uriOpts.Scheme == "https" || uriOpts.KeyFile != "" ||
				uriOpts.CertFile != "" || uriOpts.CaFile != "" || uriOpts.Ciphers != "",
			KeyFile:  uriOpts.KeyFile,
			CertFile: uriOpts.CertFile,
			CaFile:   uriOpts.CaFile,
			Ciphers:  uriOpts.Ciphers,
		},
	}
}

// NewStorageConnection determines a storage based on the opts.
func NewStorageConnection(
	connOpts ConnectOpts, opts sdkconnect.URIOpts,
) (gstorage.Storage, gsconnect.CleanupFunc, string, error) {
	etcdCfg := getEtcdCfg(connOpts, opts)

	etcdClient, errEtcd := connectEtcdClient(etcdCfg)
	if errEtcd == nil {
		driver := etcd.New(etcdClient)
		return gstorage.NewStorage(driver), func() { _ = etcdClient.Close() }, etcdStorageType, nil
	}

	tcsCfg := getTarantoolCfg(connOpts, opts)

	stor, cleanup, errTCS := NewTCSStorage(context.Background(), tcsCfg)
	if errTCS == nil {
		return stor, cleanup, tcsStorageType, nil
	}

	return nil,
		func() {},
		"",
		fmt.Errorf("failed to establish a connection to tarantool or etcd: %w, %w",
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
		return nil, err //nolint:wrapcheck // The caller names the CA directory.
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
