package cluster

import (
	"context"
	"fmt"
	"time"

	gstorage "github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/operation"
	"github.com/tarantool/go-storage/predicate"
	"github.com/tarantool/go-tarantool/v2"
)

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
	storage     gstorage.Storage
	key         string
	storageType string
	prefix      string
	timeout     time.Duration
}

// collectByRange collects kvs by prefix.
func (r *RawStorage) collectByRange(ctx context.Context, prefix string) ([]Data, error) {
	kvs, err := r.storage.Range(ctx, gstorage.WithPrefix(prefix))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from %s: %w", r.storageType, err)
	}

	if len(kvs) == 0 {
		return nil, fmt.Errorf("a configuration data not found in %s for prefix %q",
			r.storageType, r.prefix)
	}

	data := make([]Data, 0, len(kvs))
	for _, kv := range kvs {
		data = append(data, Data{
			Source:   string(kv.Key),
			Value:    kv.Value,
			Revision: kv.ModRevision,
		})
	}

	return data, nil
}

// collectByRange collects kvs by key.
func (r *RawStorage) collectByKey(ctx context.Context, key string) ([]Data, error) {
	resp, err := r.storage.Tx(ctx).Then(operation.Get([]byte(key))).Commit()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch data from %s: %w", r.storageType, err)
	}

	data := make([]Data, 0, len(resp.Results))
	for _, result := range resp.Results {
		for _, kv := range result.Values {
			data = append(data, Data{
				Source:   string(kv.Key),
				Value:    kv.Value,
				Revision: kv.ModRevision,
			})
		}
	}

	switch len(data) {
	case 0:
		return nil, fmt.Errorf("a configuration data not found in %s for key %q", r.storageType, key)
	case 1:
		return []Data{data[0]}, nil
	default:
		return nil, fmt.Errorf("too many responses (%v) from etcd for key %q", data, key)
	}
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
		return r.collectByRange(ctx, getConfigPrefix(r.prefix))
	default:
		return r.collectByKey(ctx, getConfigKey(r.prefix, r.key))
	}
}

// publishByKey put data by specific key.
func (r *RawStorage) publishByKey(ctx context.Context, key string, revision int64, data []byte) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, errDataMissing)
	}

	var predicates []predicate.Predicate
	if revision != 0 {
		predicates = append(predicates, predicate.VersionEqual([]byte(key), revision))
	}

	txn := r.storage.Tx(ctx)
	if predicates != nil {
		txn = txn.If(predicates...)
	}
	resp, err := txn.Then(operation.Put([]byte(key), data)).Commit()
	if err != nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, err)
	}
	if !resp.Succeeded {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, errWrongRevision)
	}

	return nil
}

// publishByKey put data by prefix.
func (r *RawStorage) publishByRange(ctx context.Context, prefix string, targetKey string, revision int64, data []byte) error {
	if data == nil {
		return fmt.Errorf("failed to publish data into %s: %w", r.storageType, errDataMissing)
	}

	if revision != 0 {
		return fmt.Errorf("failed to publish data into %s: target revision %d is not supported",
			r.storageType, revision)
	}

	for {
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
		kvs, err := r.storage.Range(ctx, gstorage.WithPrefix(prefix))
		if err != nil {
			return fmt.Errorf("failed to fetch data from %s: %w", r.storageType, err)
		}

		// Then we need to delete all other paths and put the configuration
		// into the target key. We do it in a single transaction to avoid
		// concurrent updates and collisions.

		var (
			predicates []predicate.Predicate
			ops        []operation.Operation
		)
		for _, kv := range kvs {
			// We need to skip the target key since some storage backends do not
			// support delete + put for the same key in a single transaction.
			if string(kv.Key) != targetKey {
				predicates = append(predicates,
					predicate.VersionEqual(kv.Key, kv.ModRevision))
				ops = append(ops, operation.Delete(kv.Key))
			}
		}

		// Fill the put part of the transaction.
		ops = append(ops, operation.Put([]byte(targetKey), data))

		txn := r.storage.Tx(ctx)
		if len(predicates) > 0 {
			txn = txn.If(predicates...)
		}

		// And try to execute the transaction.
		resp, err := txn.Then(ops...).Commit()
		if err != nil {
			return fmt.Errorf("failed to publish data into %s: %w", r.storageType, err)
		}
		if resp.Succeeded {
			return nil
		}
		// Transaction failed due to concurrent modification, retry.
	}
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
		return r.publishByRange(ctx, getConfigPrefix(r.prefix), getConfigKey(r.prefix, "all"), revision, data)
	default:
		return r.publishByKey(ctx, getConfigKey(r.prefix, r.key), revision, data)
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
	_, err := r.storage.Tx(ctx).Then(operation.Put([]byte(key), []byte(value))).Commit()
	return err
}

// Watch watches on a key and return watched events through the returned channel.
func (r *RawStorage) Watch(ctx context.Context, key string) <-chan CSWatchEvent {
	ch := make(chan CSWatchEvent)
	innerCh := r.storage.Watch(ctx, []byte(key))

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
	return ch
}

// NewStorage returns RawStorage with specified storageType.
func NewStorage(storage gstorage.Storage, prefix string, timeout time.Duration, key string, storageType string) RawStorage {
	return RawStorage{
		storage:     storage,
		key:         key,
		storageType: storageType,
		prefix:      prefix,
		timeout:     timeout,
	}
}
