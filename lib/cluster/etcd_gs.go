package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/operation"
	"github.com/tarantool/go-storage/predicate"
)

// GSEtcdAllCollector collects data from etcd via go-storage for a whole prefix.
type GSEtcdAllCollector struct {
	store   storage.Storage
	prefix  string
	timeout time.Duration
}

// NewGSEtcdAllCollector creates a new go-storage-based collector for etcd from the whole prefix.
func NewGSEtcdAllCollector(
	store storage.Storage,
	prefix string,
	timeout time.Duration,
) GSEtcdAllCollector {
	return GSEtcdAllCollector{
		store:   store,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified prefix with the specified timeout.
func (collector GSEtcdAllCollector) Collect() ([]Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	kvs, err := collector.store.Range(ctx, storage.WithPrefix(prefix))
	if err != nil {
		return nil, fmt.Errorf("%w from etcd: %w", errFetchData, err)
	}

	if len(kvs) == 0 {
		return nil, CollectEmptyError{"etcd", prefix}
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

// GSEtcdKeyCollector collects data from etcd via go-storage for a specific key.
type GSEtcdKeyCollector struct {
	store   storage.Storage
	prefix  string
	key     string
	timeout time.Duration
}

// NewGSEtcdKeyCollector creates a new go-storage-based collector for etcd from a key from a prefix.
func NewGSEtcdKeyCollector(
	store storage.Storage,
	prefix, key string,
	timeout time.Duration,
) GSEtcdKeyCollector {
	return GSEtcdKeyCollector{
		store:   store,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified path with the specified timeout.
func (collector GSEtcdKeyCollector) Collect() ([]Data, error) {
	key := getConfigKey(collector.prefix, collector.key)
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.store.Tx(ctx).Then(operation.Get([]byte(key))).Commit()
	if err != nil {
		return nil, fmt.Errorf("%w from etcd: %w", errFetchData, err)
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
		return nil, fmt.Errorf("a configuration data not found in etcd for key %q", key)
	case 1:
		return []Data{data[0]}, nil
	default:
		return nil, fmt.Errorf("too many responses (%v) from etcd for key %q", data, key)
	}
}

// GSEtcdAllDataPublisher publishes data into etcd via go-storage to a prefix.
type GSEtcdAllDataPublisher struct {
	store   storage.Storage
	prefix  string
	timeout time.Duration
}

// NewGSEtcdAllDataPublisher creates a new go-storage-based etcd publisher to a prefix.
func NewGSEtcdAllDataPublisher(
	store storage.Storage,
	prefix string,
	timeout time.Duration,
) GSEtcdAllDataPublisher {
	return GSEtcdAllDataPublisher{
		store:   store,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Publish publishes the configuration into etcd to the given prefix.
func (publisher GSEtcdAllDataPublisher) Publish(revision int64, data []byte) error {
	if revision != 0 {
		return fmt.Errorf("%w into etcd: target revision %d is not supported",
			errPublishData, revision)
	}
	if data == nil {
		return fmt.Errorf("%w into etcd: %w", errPublishData, errDataMissing)
	}

	prefix := getConfigPrefix(publisher.prefix)
	key := prefix + "all"
	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		kvs, err := publisher.store.Range(ctx, storage.WithPrefix(prefix))
		if err != nil {
			return fmt.Errorf("%w from etcd: %w", errFetchData, err)
		}

		predicates := make([]predicate.Predicate, 0, len(kvs))
		ops := make([]operation.Operation, 0, len(kvs)+1)
		for _, kv := range kvs {
			if string(kv.Key) == key {
				continue
			}

			predicates = append(predicates, predicate.VersionEqual(kv.Key, kv.ModRevision))
			ops = append(ops, operation.Delete(kv.Key))
		}
		ops = append(ops, operation.Put([]byte(key), data))

		txn := publisher.store.Tx(ctx)
		if len(predicates) > 0 {
			txn = txn.If(predicates...)
		}
		resp, err := txn.Then(ops...).Commit()
		if err != nil {
			return fmt.Errorf("%w into etcd: %w", errPutData, err)
		}
		if resp.Succeeded {
			return nil
		}
	}
}

// GSEtcdKeyDataPublisher publishes data into etcd via go-storage for a prefix and key.
type GSEtcdKeyDataPublisher struct {
	store   storage.Storage
	prefix  string
	key     string
	timeout time.Duration
}

// NewGSEtcdKeyDataPublisher creates a new go-storage-based etcd publisher for a prefix and key.
func NewGSEtcdKeyDataPublisher(
	store storage.Storage,
	prefix, key string,
	timeout time.Duration,
) GSEtcdKeyDataPublisher {
	return GSEtcdKeyDataPublisher{
		store:   store,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Publish publishes the configuration into etcd to the given prefix and key.
func (publisher GSEtcdKeyDataPublisher) Publish(revision int64, data []byte) error {
	if data == nil {
		return fmt.Errorf("%w into etcd: %w", errPublishData, errDataMissing)
	}

	key := getConfigKey(publisher.prefix, publisher.key)
	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	var predicates []predicate.Predicate
	if revision != 0 {
		predicates = append(predicates, predicate.VersionEqual([]byte(key), revision))
	}

	txn := publisher.store.Tx(ctx)
	if predicates != nil {
		txn = txn.If(predicates...)
	}
	resp, err := txn.Then(operation.Put([]byte(key), data)).Commit()
	if err != nil {
		return fmt.Errorf("%w into etcd: %w", errPutData, err)
	}
	if !resp.Succeeded {
		return fmt.Errorf("%w into etcd: %w", errPutData, errWrongRevision)
	}

	return nil
}
