package cluster

import (
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/tt/cli/connector"
)

// TarantoolAllCollector collects data from a Tarantool for a whole prefix.
type TarantoolAllCollector struct {
	evaler  connector.Evaler
	prefix  string
	timeout time.Duration
}

// NewTarantoolAllCollector creates a new collector for Tarantool from the
// whole prefix.
func NewTarantoolAllCollector(evaler connector.Evaler, prefix string,
	timeout time.Duration) TarantoolAllCollector {
	return TarantoolAllCollector{
		evaler:  evaler,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified prefix with the
// specified timeout.
func (collector TarantoolAllCollector) Collect() (*Config, error) {
	prefix := getConfigPrefix(collector.prefix)
	resp, err := tarantoolGet(collector.evaler, prefix, collector.timeout)
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("a configuration data not found in tarantool for prefix %q",
			prefix)
	}

	cconfig := NewConfig()
	for _, data := range resp.Data {
		if config, err := NewYamlCollector([]byte(data.Value)).Collect(); err != nil {
			fmtErr := "failed to decode tarantool config for key %q: %w"
			return nil, fmt.Errorf(fmtErr, data.Path, err)
		} else {
			cconfig.Merge(config)
		}
	}

	return cconfig, nil
}

// TarantoolKeyCollector collects data from a Tarantool for a separate key.
type TarantoolKeyCollector struct {
	evaler  connector.Evaler
	prefix  string
	key     string
	timeout time.Duration
}

// NewTarantoolKeyCollector creates a new collector for Tarantool from a key
// from a prefix.
func NewTarantoolKeyCollector(evaler connector.Evaler, prefix, key string,
	timeout time.Duration) TarantoolKeyCollector {
	return TarantoolKeyCollector{
		evaler:  evaler,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Collect collects a configuration from the specified path with the specified
// timeout.
func (collector TarantoolKeyCollector) Collect() (*Config, error) {
	key := getConfigPrefix(collector.prefix) + collector.key
	resp, err := tarantoolGet(collector.evaler, key, collector.timeout)
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

	config, err := NewYamlCollector([]byte(resp.Data[0].Value)).Collect()
	if err != nil {
		return nil,
			fmt.Errorf("failed to decode tarantool config for key %q: %w", key, err)
	}
	return config, err
}

// TarantoolAllDataPublisher publishes a data into Tarantool to a prefix.
type TarantoolAllDataPublisher struct {
	evaler  connector.Evaler
	prefix  string
	timeout time.Duration
}

// NewTarantoolAllDataPublisher creates a new TarantoolAllDataPublisher object
// to publish a data to Tarantool with the prefix during the timeout.
func NewTarantoolAllDataPublisher(evaler connector.Evaler,
	prefix string, timeout time.Duration) TarantoolAllDataPublisher {
	return TarantoolAllDataPublisher{
		evaler:  evaler,
		prefix:  prefix,
		timeout: timeout,
	}
}

// Publish publishes the configuration into Tarantool to the given prefix.
func (publisher TarantoolAllDataPublisher) Publish(data []byte) error {
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
	opts := connector.RequestOpts{ReadTimeout: publisher.timeout}

	_, err := publisher.evaler.Eval("return config.storage.txn(...)", args, opts)
	if err != nil {
		return fmt.Errorf("failed to put data into tarantool: %w", err)
	}
	return nil
}

// TarantoolKeyDataPublisher publishes a data into Tarantool for a prefix
// and a key.
type TarantoolKeyDataPublisher struct {
	evaler  connector.Evaler
	prefix  string
	key     string
	timeout time.Duration
}

// NewTarantoolKeyDataPublisher creates a new TarantoolKeyDataPublisher object
// to publish a data to Tarantool with the prefix and key during the timeout.
func NewTarantoolKeyDataPublisher(evaler connector.Evaler,
	prefix, key string, timeout time.Duration) TarantoolKeyDataPublisher {
	return TarantoolKeyDataPublisher{
		evaler:  evaler,
		prefix:  prefix,
		key:     key,
		timeout: timeout,
	}
}

// Publish publishes the configuration into Tarantool to the given prefix and
// key.
func (publisher TarantoolKeyDataPublisher) Publish(data []byte) error {
	key := getConfigPrefix(publisher.prefix) + publisher.key
	args := []any{key, string(data)}
	opts := connector.RequestOpts{ReadTimeout: publisher.timeout}

	_, err := publisher.evaler.Eval("return config.storage.put(...)", args, opts)
	if err != nil {
		return fmt.Errorf("failed to put data into tarantool: %w", err)
	}
	return nil
}

type tarantoolResponse struct {
	Data []struct {
		Path  string
		Value string
	}
}

func tarantoolGet(evaler connector.Evaler,
	path string, timeout time.Duration) (tarantoolResponse, error) {
	resp := tarantoolResponse{}

	args := []any{path}
	opts := connector.RequestOpts{ReadTimeout: timeout}
	data, err := evaler.Eval("return config.storage.get(...)", args, opts)
	if err != nil {
		return resp, fmt.Errorf("failed to fetch data from tarantool: %w", err)
	}
	if len(data) != 1 {
		return resp, fmt.Errorf("unexpected response from tarantool: %q", data)
	}

	if err := mapstructure.Decode(data[0], &resp); err != nil {
		return resp, fmt.Errorf("failed to map response from tarantool: %q", data[0])
	}

	return resp, nil
}
