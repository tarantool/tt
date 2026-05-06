package cluster

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	gstorage "github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/integrity"
	"github.com/tarantool/go-storage/marshaller"
	"github.com/tarantool/go-tarantool/v2"
)

type StorageDataType = []byte

const (
	etcdStorageType = "etcd"
	tcsStorageType  = "tarantool"
)

// dummyDoerWatcher implements DoerWatcher without watcher function.
type dummyDoerWatcher struct {
	conn tarantool.Doer
}

// Do performs a request.
func (b dummyDoerWatcher) Do(req tarantool.Request) (fut *tarantool.Future) {
	return b.conn.Do(req)
}

// NewWatcher is just a stub for DoeWatcher.
func (b dummyDoerWatcher) NewWatcher(key string, callback tarantool.WatchCallback) (tarantool.Watcher, error) {
	return nil, nil
}

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
	close       func() error
	storage     *integrity.Store[StorageDataType]
	codec       *integrity.Codec[StorageDataType]
	key         string
	storageType string
	prefix      string
	timeout     time.Duration
}

func (r *RawStorage) normalizeName(name string) string {
	if name == "" {
		return ""
	}

	if r.prefix == "" {
		return strings.TrimPrefix(name, "/")
	}

	configPrefix := r.prefix
	if trimmed, ok := strings.CutPrefix(name, configPrefix); ok {
		return trimmed
	}

	configPrefix = strings.TrimSuffix(configPrefix, "/")
	if trimmed, ok := strings.CutPrefix(name, configPrefix); ok {
		return strings.TrimPrefix(trimmed, "/")
	}

	return strings.TrimPrefix(name, "/")
}

func (r *RawStorage) sourceName(name string) string {
	name = r.normalizeName(name)
	if name == "" {
		return r.prefix
	}
	if strings.HasSuffix(r.prefix, "/") {
		return r.prefix + name
	}
	return r.prefix + "/" + name
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
) RawStorage {
	basePrefix := getConfigPrefixNew(prefix)

	codec := integrity.NewCodecBuilder[StorageDataType]().
		WithMarshaller(marshaller.NewTypedBytesMarshaller())

	codecBuild, err := applyIntegrityBuilderOptions(codec, integrityOpts).Build()
	if err != nil {
		panic(err)
	}

	storage, err = gstorage.Prefixed(basePrefix, storage)
	if err != nil {
		panic(err)
	}

	store := codecBuild.Bind(storage)

	return RawStorage{
		storage:     store,
		codec:       codecBuild,
		key:         key,
		storageType: storageType,
		timeout:     timeout,
		prefix:      basePrefix,
	}
}
