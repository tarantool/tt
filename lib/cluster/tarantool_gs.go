package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/operation"
	"github.com/tarantool/go-storage/predicate"
)

// GSTarantoolAllCollector collects data from tarantool via go-storage for a whole prefix.
type GSTarantoolAllCollector struct {
	store   storage.Storage
	prefix  string
	timeout time.Duration
}

// NewGSTarantoolAllCollector creates a new go-storage-based collector for tarantool from the whole prefix.
func NewGSTarantoolAllCollector(
	store storage.Storage,
	prefix string,
	timeout time.Duration,
) GSTarantoolAllCollector {
	return GSTarantoolAllCollector{
		store:   store,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified prefix with the specified timeout.
func (collector GSTarantoolAllCollector) Collect() ([]Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	kvs, err := collector.store.Range(ctx, storage.WithPrefix(prefix))
	if err != nil {
		return nil, fmt.Errorf("%w from tarantool: %w", errFetchData, err)
	}

	if len(kvs) == 0 {
		return nil, CollectEmptyError{"tarantool", prefix}
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

// GSTarantoolKeyCollector collects data from tarantool via go-storage for a specific key.
type GSTarantoolKeyCollector struct {
	store   storage.Storage
	prefix  string
	key     string
	timeout time.Duration
}

// NewGSTarantoolKeyCollector creates a new go-storage-based collector for tarantool from a key from a prefix.
func NewGSTarantoolKeyCollector(
	store storage.Storage,
	prefix, key string,
	timeout time.Duration,
) GSTarantoolKeyCollector {
	return GSTarantoolKeyCollector{
		store:   store,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified path with the specified timeout.
func (collector GSTarantoolKeyCollector) Collect() ([]Data, error) {
	key := getConfigKey(collector.prefix, collector.key)
	ctx := context.Background()
	if collector.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, collector.timeout)
		defer cancel()
	}

	resp, err := collector.store.Tx(ctx).Then(operation.Get([]byte(key))).Commit()
	if err != nil {
		return nil, fmt.Errorf("%w from tarantool: %w", errFetchData, err)
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
		return nil, fmt.Errorf("a configuration data not found in tarantool for key %q", key)
	case 1:
		return []Data{data[0]}, nil
	default:
		return nil, fmt.Errorf("too many responses (%v) from tarantool for key %q", data, key)
	}
}

// GSTarantoolAllDataPublisher publishes data into tarantool via go-storage to a prefix.
type GSTarantoolAllDataPublisher struct {
	store   storage.Storage
	prefix  string
	timeout time.Duration
}

// NewGSTarantoolAllDataPublisher creates a new go-storage-based tarantool publisher to a prefix.
func NewGSTarantoolAllDataPublisher(
	store storage.Storage,
	prefix string,
	timeout time.Duration,
) GSTarantoolAllDataPublisher {
	return GSTarantoolAllDataPublisher{
		store:   store,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Publish publishes the configuration into tarantool to the given prefix.
func (publisher GSTarantoolAllDataPublisher) Publish(revision int64, data []byte) error {
	if revision != 0 {
		return fmt.Errorf("%w into tarantool: target revision %d is not supported",
			errPublishData, revision)
	}
	if data == nil {
		return fmt.Errorf("%w into tarantool: %w", errPublishData, errDataMissing)
	}

	prefix := getConfigPrefix(publisher.prefix)
	key := getConfigKey(publisher.prefix, "all")
	ctx := context.Background()
	if publisher.timeout != 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, publisher.timeout)
		defer cancel()
	}

	_, err := publisher.store.Tx(ctx).Then(
		operation.Delete([]byte(prefix)),
		operation.Put([]byte(key), data),
	).Commit()
	if err != nil {
		return fmt.Errorf("%w into tarantool: %w", errPutData, err)
	}

	return nil
}

// GSTarantoolKeyDataPublisher publishes data into tarantool via go-storage for a prefix and key.
type GSTarantoolKeyDataPublisher struct {
	store   storage.Storage
	prefix  string
	key     string
	timeout time.Duration
}

// NewGSTarantoolKeyDataPublisher creates a new go-storage-based tarantool publisher for a prefix and key.
func NewGSTarantoolKeyDataPublisher(
	store storage.Storage,
	prefix, key string,
	timeout time.Duration,
) GSTarantoolKeyDataPublisher {
	return GSTarantoolKeyDataPublisher{
		store:   store,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Publish publishes the configuration into tarantool to the given prefix and key.
func (publisher GSTarantoolKeyDataPublisher) Publish(revision int64, data []byte) error {
	if data == nil {
		return fmt.Errorf("%w into tarantool: %w", errPublishData, errDataMissing)
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
	_, err := txn.Then(operation.Put([]byte(key), data)).Commit()
	if err != nil {
		return fmt.Errorf("%w into tarantool: %w", errPutData, err)
	}

	return nil
}
