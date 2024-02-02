package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/go-tarantool"
	"github.com/tarantool/tt/cli/integrity"
)

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
func (collector TarantoolAllCollector) Collect() ([]integrity.Data, error) {
	prefix := getConfigPrefix(collector.prefix)
	resp, err := tarantoolGet(collector.conn, prefix, collector.timeout)
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("a configuration data not found in tarantool for prefix %q",
			prefix)
	}

	collected := []integrity.Data{}
	for _, data := range resp.Data {
		collected = append(collected, integrity.Data{
			Source:   data.Path,
			Value:    []byte(data.Value),
			Revision: data.ModRevision,
		})
	}

	return collected, nil
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
func (collector TarantoolKeyCollector) Collect() ([]integrity.Data, error) {
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

	return []integrity.Data{
		{
			Source:   key,
			Value:    []byte(resp.Data[0].Value),
			Revision: resp.Data[0].ModRevision,
		},
	}, err
}

// TarantoolAllDataPublisher publishes a data into Tarantool to a prefix.
type TarantoolAllDataPublisher struct {
	conn    tarantool.Connector
	prefix  string
	timeout time.Duration
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

// Publish publishes the configuration into Tarantool to the given prefix.
func (publisher TarantoolAllDataPublisher) Publish(revision int64, data []byte) error {
	if revision != 0 {
		return fmt.Errorf(
			"failed to publish data into tarantool: target revision %d is not supported",
			revision)
	}
	prefix := getConfigPrefix(publisher.prefix)
	key := prefix + "all"
	args := []any{
		map[any]any{
			"on_success": []any{
				[]any{"delete", prefix},
				[]any{"put", key, string(data)},
			},
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
	conn    tarantool.Connector
	prefix  string
	key     string
	timeout time.Duration
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

// Publish publishes the configuration into Tarantool to the given prefix and
// key.
// If passed revision is not 0, the data will be published only if target key revision the same.
func (publisher TarantoolKeyDataPublisher) Publish(revision int64, data []byte) error {
	key := getConfigPrefix(publisher.prefix) + publisher.key

	txn := map[any]any{
		"on_success": []any{
			[]any{"put", key, string(data)},
		},
	}
	if revision != 0 {
		txn["predicates"] = []any{"mod_revision", "==", revision}
	}
	args := []any{txn}

	_, err := tarantoolCall(publisher.conn, "config.storage.txn", args, publisher.timeout)
	if err != nil {
		return fmt.Errorf("failed to put data into tarantool: %w", err)
	}
	return nil
}

type tarantoolResponse struct {
	Data []struct {
		Path        string
		Value       string
		ModRevision int64 `mapstructure:"mod_revision"`
	}
}

func tarantoolGet(conn tarantool.Connector,
	path string, timeout time.Duration) (tarantoolResponse, error) {
	resp := tarantoolResponse{}

	args := []any{path}

	data, err := tarantoolCall(conn, "config.storage.get", args, timeout)
	if err != nil {
		return resp, fmt.Errorf("failed to fetch data from tarantool: %w", err)
	}
	if len(data) != 1 {
		return resp, fmt.Errorf("unexpected response from tarantool: %q", data)
	}

	rawResp := data[0][0]
	if err := mapstructure.Decode(rawResp, &resp); err != nil {
		return resp, fmt.Errorf("failed to map response from tarantool: %q", rawResp)
	}

	return resp, nil
}
