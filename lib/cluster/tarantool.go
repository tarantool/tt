package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/go-tarantool"
)

// TarantoolAllCollector collects data from a Tarantool for a whole prefix.
type TarantoolAllCollector struct {
	conn    tarantool.Connector
	prefix  string
	timeout time.Duration
}

// NewTarantoolAllCollector creates a new collector for Tarantool from the
// whole prefix.
func NewTarantoolAllCollector(conn tarantool.Connector, prefix string,
	timeout time.Duration) TarantoolAllCollector {
	return TarantoolAllCollector{
		conn:    conn,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified prefix with the
// specified timeout.
func (collector TarantoolAllCollector) Collect() ([]Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	resp, err := tarantoolGet(collector.conn, prefix, collector.timeout)
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("a configuration data not found in tarantool for prefix %q",
			prefix)
	}

	collected := []Data{}
	for _, data := range resp.Data {
		collected = append(collected, Data{
			Source:   data.Path,
			Value:    []byte(data.Value),
			Revision: data.ModRevision,
		})
	}

	return collected, nil
}

// IntegrityTarantoolAllCollector collects data from a Tarantool for a prefix
// with integrity checks.
type IntegrityTarantoolAllCollector struct {
	checkFunc CheckFunc
	conn      tarantool.Connector
	prefix    string
	timeout   time.Duration
}

// NewIntegrityTarantoolAllCollector creates a new collector for Tarantool from the
// whole prefix.
func NewIntegrityTarantoolAllCollector(checkFunc CheckFunc,
	conn tarantool.Connector, prefix string,
	timeout time.Duration) IntegrityTarantoolAllCollector {
	return IntegrityTarantoolAllCollector{
		checkFunc: checkFunc,
		conn:      conn,
		prefix:    prefix,
		timeout:   timeout,
	}
}

// Collect collects a configuration from the specified prefix with the specified
// timeout.
func (collector IntegrityTarantoolAllCollector) Collect() ([]Data, error) {
	var (
		valuesPrefix = getConfigPrefix(collector.prefix)
		hashesPrefix = getHashesPrefix(collector.prefix)
		signsPrefix  = getSignPrefix(collector.prefix)
	)
	resp, err := tarantoolTxnGet(collector.conn, []string{
		valuesPrefix,
		hashesPrefix,
		signsPrefix,
	}, collector.timeout)
	if err != nil {
		return nil, err
	}

	type valueNode struct {
		Value  []byte
		Hashes map[string][]byte
		Sig    []byte
	}
	keys := []string{} // We need to keep the original order of keys.
	nodes := map[string]valueNode{}
	for _, response := range resp.Data.Responses {
		for _, data := range response {
			var (
				hash, key string
				ok        bool
				update    valueNode
			)

			if key, ok = strings.CutPrefix(data.Path, valuesPrefix); ok {
				update.Value = []byte(data.Value)
			} else if ok, hash, key = parseHashPath(data.Path, collector.prefix); ok {
				update.Hashes = map[string][]byte{
					hash: []byte(data.Value),
				}
			} else if key, ok = strings.CutPrefix(data.Path, signsPrefix); ok {
				update.Sig = []byte(data.Value)
			} else {
				continue
			}

			if node, ok := nodes[key]; ok {
				if len(update.Value) > 0 {
					node.Value = update.Value
				}
				if update.Hashes != nil {
					if node.Hashes == nil {
						node.Hashes = update.Hashes
					} else {
						for k, v := range update.Hashes {
							node.Hashes[k] = v
						}
					}
				}
				if len(update.Sig) > 0 {
					node.Sig = update.Sig
				}
				nodes[key] = node
			} else {
				nodes[key] = update
				keys = append(keys, key)
			}
		}
	}

	data := []Data{}
	for _, key := range keys {
		node := nodes[key]
		if len(node.Value) == 0 {
			continue
		}
		fullKey := getConfigKey(collector.prefix, key)

		err := collector.checkFunc(node.Value, node.Hashes, node.Sig)
		if err != nil {
			return nil, fmt.Errorf("failed to perform integrity checks for key %q: %w",
				fullKey, err)
		}

		data = append(data, Data{
			Source: fullKey,
			Value:  []byte(node.Value),
		})
	}

	return data, nil
}

// TarantoolKeyCollector collects data from a Tarantool for a separate key.
type TarantoolKeyCollector struct {
	conn    tarantool.Connector
	prefix  string
	key     string
	timeout time.Duration
}

// NewTarantoolKeyCollector creates a new collector for Tarantool from a key
// from a prefix.
func NewTarantoolKeyCollector(conn tarantool.Connector, prefix, key string,
	timeout time.Duration) TarantoolKeyCollector {
	return TarantoolKeyCollector{
		conn:    conn,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified path with the specified
// timeout.
func (collector TarantoolKeyCollector) Collect() ([]Data, error) {
	key := getConfigPrefix(collector.prefix) + collector.key
	resp, err := tarantoolGet(collector.conn, key, collector.timeout)
	if err != nil {
		return nil, err
	}

	switch {
	case len(resp.Data) == 0:
		return nil, fmt.Errorf("a configuration data not found in tarantool for key %q",
			key)
	case len(resp.Data) > 1:
		// It should not happen, but we need to be sure to avoid a null pointer
		// dereference.
		return nil, fmt.Errorf("too many responses (%v) from tarantool for key %q",
			resp, key)
	}

	return []Data{
		{
			Source:   key,
			Value:    []byte(resp.Data[0].Value),
			Revision: resp.Data[0].ModRevision,
		},
	}, err
}

// IntegrityTarantoolKeyCollector collects data from a Tarantool for a separate
// key and makes integrity checks.
type IntegrityTarantoolKeyCollector struct {
	checkFunc CheckFunc
	conn      tarantool.Connector
	prefix    string
	key       string
	timeout   time.Duration
}

// NewIntegrityTarantoolKeyCollector creates a new collector for Tarantool from
// a key from a prefix with integrity checks.
func NewIntegrityTarantoolKeyCollector(checkFunc CheckFunc,
	conn tarantool.Connector, prefix, key string,
	timeout time.Duration) IntegrityTarantoolKeyCollector {
	return IntegrityTarantoolKeyCollector{
		checkFunc: checkFunc,
		conn:      conn,
		prefix:    prefix,
		key:       key,
		timeout:   timeout,
	}
}

// Collect collects a configuration from the specified key with the specified
// timeout.
func (collector IntegrityTarantoolKeyCollector) Collect() ([]Data, error) {
	var (
		valueKey     = getConfigKey(collector.prefix, collector.key)
		hashesPrefix = getHashesPrefix(collector.prefix)
		sigKey       = getSignKey(collector.prefix, collector.key)
	)
	resp, err := tarantoolTxnGet(collector.conn, []string{
		valueKey,
		hashesPrefix,
		sigKey,
	}, collector.timeout)
	if err != nil {
		return nil, err
	}

	var (
		value  []byte
		hashes = make(map[string][]byte)
		sig    []byte
	)
	for _, response := range resp.Data.Responses {
		for _, data := range response {
			switch data.Path {
			case valueKey:
				value = []byte(data.Value)
			case sigKey:
				sig = []byte(data.Value)
			default:
				if ok, hash, key := parseHashPath(data.Path, collector.prefix); ok {
					if key == collector.key {
						hashes[hash] = []byte(data.Value)
					}
				}
			}
		}
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("value for key %q not found", valueKey)
	}

	if err := collector.checkFunc(value, hashes, sig); err != nil {
		return nil, fmt.Errorf("failed to perform integrity checks for key %q: %w",
			valueKey, err)
	}

	return []Data{
		Data{
			Source: valueKey,
			Value:  []byte(value),
		},
	}, err
}

// TarantoolAllDataPublisher publishes a data into Tarantool to a prefix.
type TarantoolAllDataPublisher struct {
	conn     tarantool.Connector
	prefix   string
	signFunc SignFunc
	timeout  time.Duration
}

// NewTarantoolAllDataPublisher creates a new TarantoolAllDataPublisher object
// to publish a data to Tarantool with the prefix during the timeout.
func NewTarantoolAllDataPublisher(conn tarantool.Connector,
	prefix string, timeout time.Duration) TarantoolAllDataPublisher {
	return TarantoolAllDataPublisher{
		conn:    conn,
		prefix:  prefix,
		timeout: timeout,
	}
}

// NewIntegrityTarantoolAllDataPublisher creates a new TarantoolAllDataPublisher
// object to publish a signed data to Tarantool with the prefix during the
// timeout.
func NewIntegrityTarantoolAllDataPublisher(signFunc SignFunc,
	conn tarantool.Connector,
	prefix string,
	timeout time.Duration) TarantoolAllDataPublisher {
	return TarantoolAllDataPublisher{
		conn:     conn,
		prefix:   prefix,
		signFunc: signFunc,
		timeout:  timeout,
	}
}

// Publish publishes the configuration into Tarantool to the given prefix.
func (publisher TarantoolAllDataPublisher) Publish(revision int64, data []byte) error {
	const key = "all"

	if revision != 0 {
		return fmt.Errorf(
			"failed to publish data into tarantool: target revision %d is not supported",
			revision)
	}
	prefix := getConfigPrefix(publisher.prefix)
	targetKey := getConfigKey(publisher.prefix, "all")
	onSuccess := []any{
		[]any{"delete", prefix},
		[]any{"put", targetKey, string(data)},
	}
	if publisher.signFunc != nil {
		hashes, sign, err := publisher.signFunc(data)
		if err != nil {
			return fmt.Errorf("failed to sign data: %w", err)
		}
		onSuccess = append(onSuccess,
			[]any{"delete", getHashesPrefix(publisher.prefix)})
		for hash, value := range hashes {
			targetKey := getHashesKey(publisher.prefix, hash, key)
			onSuccess = append(onSuccess, []any{"put", targetKey, string(value)})
		}
		onSuccess = append(onSuccess,
			[]any{"delete", getSignPrefix(publisher.prefix)})
		onSuccess = append(onSuccess,
			[]any{"put", getSignKey(publisher.prefix, key), string(sign)})
	}
	args := []any{
		map[any]any{
			"on_success": onSuccess,
		},
	}

	_, err := tarantoolCall(publisher.conn, "config.storage.txn", args, publisher.timeout)
	if err != nil {
		return fmt.Errorf("failed to put data into tarantool: %w", err)
	}
	return nil
}

// TarantoolKeyDataPublisher publishes a data into Tarantool for a prefix
// and a key.
type TarantoolKeyDataPublisher struct {
	conn     tarantool.Connector
	prefix   string
	key      string
	signFunc SignFunc
	timeout  time.Duration
}

// NewTarantoolKeyDataPublisher creates a new TarantoolKeyDataPublisher object
// to publish a data to Tarantool with the prefix and key during the timeout.
func NewTarantoolKeyDataPublisher(conn tarantool.Connector,
	prefix, key string, timeout time.Duration) TarantoolKeyDataPublisher {
	return TarantoolKeyDataPublisher{
		conn:    conn,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// NewTarantoolKeyDataPublisher creates a new TarantoolKeyDataPublisher object
// to publish a signed data to Tarantool with the prefix and key during the
// timeout.
func NewIntegrityTarantoolKeyDataPublisher(signFunc SignFunc,
	conn tarantool.Connector,
	prefix, key string,
	timeout time.Duration) TarantoolKeyDataPublisher {
	return TarantoolKeyDataPublisher{
		conn:     conn,
		prefix:   prefix,
		key:      key,
		signFunc: signFunc,
		timeout:  timeout,
	}
}

// Publish publishes the configuration into Tarantool to the given prefix and
// key.
// If passed revision is not 0, the data will be published only if target key revision the same.
func (publisher TarantoolKeyDataPublisher) Publish(revision int64, data []byte) error {
	key := getConfigKey(publisher.prefix, publisher.key)

	onSuccess := []any{
		[]any{"put", key, string(data)},
	}
	if publisher.signFunc != nil {
		hashes, sign, err := publisher.signFunc(data)
		if err != nil {
			return fmt.Errorf("failed to sign data: %w", err)
		}
		for hash, value := range hashes {
			targetKey := getHashesKey(publisher.prefix, hash, publisher.key)
			onSuccess = append(onSuccess, []any{"put", targetKey, string(value)})
		}
		onSuccess = append(onSuccess,
			[]any{"put", getSignKey(publisher.prefix, publisher.key), string(sign)})
	}

	txn := map[any]any{
		"on_success": onSuccess,
	}

	if revision != 0 {
		txn["predicates"] = []any{[]any{"mod_revision", "==", revision, key}}
	}
	args := []any{txn}

	_, err := tarantoolCall(publisher.conn, "config.storage.txn", args, publisher.timeout)
	if err != nil {
		return fmt.Errorf("failed to put data into tarantool: %w", err)
	}
	return nil
}

// tarantoolTxnResponse is a response of a txn get requests.
type tarantoolTxnGetResponse struct {
	Data struct {
		Responses [][]struct {
			Path  string
			Value string
		}
	}
}

// tarantoolGetResponse is a response of a get request.
type tarantoolGetResponse struct {
	Data []struct {
		Path        string
		Value       string
		ModRevision int64 `mapstructure:"mod_revision"`
	}
}

// tarantoolCall retursns result of a function call via tarantool connector.
func tarantoolCall(conn tarantool.Connector,
	fun string, args []any, timeout time.Duration) ([][]any, error) {
	req := tarantool.NewCallRequest(fun).Args(args)

	if timeout != 0 {
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		req = req.Context(ctx)
	}

	var result [][]any
	if err := conn.Do(req).GetTyped(&result); err != nil {
		return nil, err
	}

	return result, nil
}

func tarantoolGet(conn tarantool.Connector,
	path string, timeout time.Duration) (tarantoolGetResponse, error) {
	resp := tarantoolGetResponse{}

	args := []any{path}

	data, err := tarantoolCall(conn, "config.storage.get", args, timeout)
	if err != nil {
		return resp, fmt.Errorf("failed to fetch data from tarantool: %w", err)
	}
	if len(data) != 1 || len(data[0]) == 0 {
		return resp, fmt.Errorf("unexpected response from tarantool: %q", data)
	}

	if err := mapstructure.Decode(data[0][0], &resp); err != nil {
		return resp, fmt.Errorf("failed to map response from tarantool: %q", data[0])
	}

	return resp, nil
}

// tarantoolTxnGet returns a data from a tarantool config storage for a set
// of prefixes and keys.
func tarantoolTxnGet(conn tarantool.Connector,
	paths []string, timeout time.Duration) (tarantoolTxnGetResponse, error) {
	resp := tarantoolTxnGetResponse{}

	onSuccess := []any{}
	for _, path := range paths {
		onSuccess = append(onSuccess, []any{"get", path})
	}
	args := []any{
		map[any]any{
			"on_success": onSuccess,
		},
	}

	data, err := tarantoolCall(conn, "config.storage.txn", args, timeout)

	if err != nil {
		return resp, fmt.Errorf("failed to fetch data from tarantool: %w", err)
	}

	if len(data) != 1 || len(data[0]) == 0 {
		return resp, fmt.Errorf("unexpected response from tarantool: %q", data)
	}

	if err := mapstructure.Decode(data[0][0], &resp); err != nil {
		return resp, fmt.Errorf("failed to map response from tarantool: %q", data[0])
	}
	if len(resp.Data.Responses) == 0 {
		return resp, fmt.Errorf("unexpected response from tarantool: %q", data[0])
	}

	return resp, nil
}
