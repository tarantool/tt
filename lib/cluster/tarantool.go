package cluster

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/driver/tcs"
	"github.com/tarantool/go-storage/integrity"
	"github.com/tarantool/go-storage/marshaller"
	"github.com/tarantool/go-tarantool/v2"
	libconnect "github.com/tarantool/tt/lib/connect"
	"github.com/tarantool/tt/lib/dial"
)

// TarantoolAllCollector collects data from a Tarantool for a whole prefix.
type TarantoolAllCollector struct {
	conn    tarantool.Doer
	prefix  string
	timeout time.Duration
}

// NewTarantoolAllCollector creates a new collector for Tarantool from the
// whole prefix.
func NewTarantoolAllCollector(conn tarantool.Doer, prefix string,
	timeout time.Duration,
) TarantoolAllCollector {
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
		return nil, CollectEmptyError{"tarantool", prefix}
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

// TarantoolKeyCollector collects data from a Tarantool for a separate key.
type TarantoolKeyCollector struct {
	conn    tarantool.Doer
	prefix  string
	key     string
	timeout time.Duration
}

// NewTarantoolKeyCollector creates a new collector for Tarantool from a key
// from a prefix.
func NewTarantoolKeyCollector(conn tarantool.Doer, prefix, key string,
	timeout time.Duration,
) TarantoolKeyCollector {
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

// TarantoolAllDataPublisher publishes a data into Tarantool to a prefix.
type TarantoolAllDataPublisher struct {
	conn    tarantool.Doer
	prefix  string
	timeout time.Duration
}

// NewTarantoolAllDataPublisher creates a new TarantoolAllDataPublisher object
// to publish a data to Tarantool with the prefix during the timeout.
func NewTarantoolAllDataPublisher(conn tarantool.Doer,
	prefix string, timeout time.Duration,
) TarantoolAllDataPublisher {
	return TarantoolAllDataPublisher{
		conn:    conn,
		prefix:  prefix,
		timeout: timeout,
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
	conn    tarantool.Doer
	prefix  string
	key     string
	timeout time.Duration
}

// NewTarantoolKeyDataPublisher creates a new TarantoolKeyDataPublisher object
// to publish a data to Tarantool with the prefix and key during the timeout.
func NewTarantoolKeyDataPublisher(conn tarantool.Doer,
	prefix, key string, timeout time.Duration,
) TarantoolKeyDataPublisher {
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
	key := getConfigKey(publisher.prefix, publisher.key)

	onSuccess := []any{
		[]any{"put", key, string(data)},
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

// tarantoolCall returns result of a function call via tarantool connector.
func tarantoolCall(conn tarantool.Doer,
	fun string, args []any, timeout time.Duration,
) ([]any, error) {
	req := tarantool.NewCallRequest(fun).Args(args)

	if timeout != 0 {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		req = req.Context(ctx)
	}

	var result []any
	if err := conn.Do(req).GetTyped(&result); err != nil {
		return nil, err
	}

	return result, nil
}

func tarantoolGet(conn tarantool.Doer,
	path string, timeout time.Duration,
) (tarantoolGetResponse, error) {
	resp := tarantoolGetResponse{}

	args := []any{path}

	data, err := tarantoolCall(conn, "config.storage.get", args, timeout)
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

// tarantoolTxnGet returns a data from a tarantool config storage for a set
// of prefixes and keys.
func tarantoolTxnGet(conn tarantool.Doer,
	paths []string, timeout time.Duration,
) (tarantoolTxnGetResponse, error) {
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

	if len(data) != 1 {
		return resp, fmt.Errorf("unexpected response from tarantool: %q", data)
	}

	if err := mapstructure.Decode(data[0], &resp); err != nil {
		return resp, fmt.Errorf("failed to map response from tarantool: %q", data[0])
	}
	if len(resp.Data.Responses) == 0 {
		return resp, fmt.Errorf("unexpected response from tarantool: %q", data[0])
	}

	return resp, nil
}

func ConnectTarantool(uriOpts libconnect.UriOpts,
	connOpts ConnectOpts,
) (tarantool.Connector, error) {
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

	dialOpts := dial.Opts{
		Address:     fmt.Sprintf("tcp://%s", uriOpts.Host),
		User:        uriOpts.Username,
		Password:    uriOpts.Password,
		SslKeyFile:  uriOpts.KeyFile,
		SslCertFile: uriOpts.CertFile,
		SslCaFile:   uriOpts.CaFile,
		SslCiphers:  uriOpts.Ciphers,
	}

	dialer, err := dial.New(dialOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tarantool: %w", err)
	}

	connectorOpts := tarantool.Opts{
		Timeout: uriOpts.Timeout,
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

func connectTarantoolCS(uriOpts libconnect.UriOpts, connOpts ConnectOpts) (CSConnection, error) {
	conn, err := ConnectTarantool(uriOpts, connOpts)
	if err != nil {
		return nil, err
	}

	driver := tcs.New(conn)
	storage := storage.NewStorage(driver)

	codec, err := integrity.NewCodecBuilder[StorageDataType]().
		WithMarshaller(marshaller.NewTypedBytesMarshaller()).
		Build()
	if err != nil {
		return nil, err
	}

	store := codec.Bind(storage)

	return &RawStorage{
		storage:     store,
		codec:       codec,
		storageType: tcsStorageType,
		close: func() error {
			return conn.Close()
		},
	}, nil
}
