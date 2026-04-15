package cmd //nolint:testpackage // Tests exercise package-private worker storage operations.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-storage/v2"
	"github.com/tarantool/go-storage/v2/kv"
	"github.com/tarantool/go-storage/v2/locker"
	"github.com/tarantool/go-storage/v2/operation"
	"github.com/tarantool/go-storage/v2/predicate"
	"github.com/tarantool/go-storage/v2/tx"
	"github.com/tarantool/go-storage/v2/watch"
)

func TestParseWorkerPath(t *testing.T) {
	cases := []struct {
		name           string
		urlPath        string
		expectedPrefix string
		expectedHost   string
		expectedWorker string
		expectedErr    string
	}{
		{
			name:           "simple path",
			urlPath:        "/prefix/host1/worker1",
			expectedPrefix: "/prefix",
			expectedHost:   "host1",
			expectedWorker: "worker1",
		},
		{
			name:           "nested prefix",
			urlPath:        "/tdb-workers/tdb-cluster/host1/http-server-1",
			expectedPrefix: "/tdb-workers/tdb-cluster",
			expectedHost:   "host1",
			expectedWorker: "http-server-1",
		},
		{
			name:           "deeply nested prefix",
			urlPath:        "/a/b/c/d/host/worker",
			expectedPrefix: "/a/b/c/d",
			expectedHost:   "host",
			expectedWorker: "worker",
		},
		{
			name:           "minimal path",
			urlPath:        "/host/worker",
			expectedPrefix: "/",
			expectedHost:   "host",
			expectedWorker: "worker",
		},
		{
			name:           "path with trailing slash",
			urlPath:        "/prefix/host/worker/",
			expectedPrefix: "/prefix",
			expectedHost:   "host",
			expectedWorker: "worker",
		},
		{
			name:        "empty path",
			urlPath:     "",
			expectedErr: "URL path must not be empty",
		},
		{
			name:        "single segment",
			urlPath:     "/worker",
			expectedErr: "URL path must contain at least a host-name and a worker-name",
		},
		{
			name:        "single segment no slash",
			urlPath:     "worker",
			expectedErr: "URL path must contain at least a host-name and a worker-name",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			prefix, host, worker, err := ParseWorkerPath(testCase.urlPath)
			if testCase.expectedErr != "" {
				require.ErrorContains(t, err, testCase.expectedErr)

				return
			}

			require.NoError(t, err)
			require.Equal(t, testCase.expectedPrefix, prefix)
			require.Equal(t, testCase.expectedHost, host)
			require.Equal(t, testCase.expectedWorker, worker)
		})
	}
}

func TestBuildWorkerStorageKey(t *testing.T) {
	cases := []struct {
		name        string
		prefix      string
		hostName    string
		workerName  string
		expectedKey string
	}{
		{
			name:        "simple",
			prefix:      "/tdb-workers/tdb-cluster",
			hostName:    "host1",
			workerName:  "http-server-1",
			expectedKey: "/tdb-workers/tdb-cluster/instances/host1/http-server-1",
		},
		{
			name:        "prefix with trailing slash",
			prefix:      "/tdb-workers/tdb-cluster/",
			hostName:    "host1",
			workerName:  "worker1",
			expectedKey: "/tdb-workers/tdb-cluster/instances/host1/worker1",
		},
		{
			name:        "root prefix",
			prefix:      "/",
			hostName:    "host",
			workerName:  "worker",
			expectedKey: "/instances/host/worker",
		},
		{
			name:        "empty prefix",
			prefix:      "",
			hostName:    "host",
			workerName:  "worker",
			expectedKey: "/instances/host/worker",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			key := BuildWorkerStorageKey(testCase.prefix, testCase.hostName, testCase.workerName)
			require.Equal(t, testCase.expectedKey, key)
		})
	}
}

type mockTx struct {
	results    []tx.RequestResponse
	succeeded  bool
	err        error
	operations []operation.Operation
	predicates []predicate.Predicate
}

func (m *mockTx) If(predicates ...predicate.Predicate) tx.Tx {
	m.predicates = predicates
	return m
}

func (m *mockTx) Then(operations ...operation.Operation) tx.Tx {
	m.operations = operations
	return m
}

func (m *mockTx) Else(operations ...operation.Operation) tx.Tx {
	return m
}

func (m *mockTx) Commit() (tx.Response, error) {
	if m.err != nil {
		return tx.Response{}, m.err
	}

	return tx.Response{
		Succeeded: m.succeeded,
		Results:   m.results,
	}, nil
}

type mockStorage struct {
	tx *mockTx
}

func (m *mockStorage) Watch(
	ctx context.Context,
	key []byte,
) <-chan watch.Event {
	ch := make(chan watch.Event)
	close(ch)

	return ch
}

func (m *mockStorage) Tx(ctx context.Context) tx.Tx {
	return m.tx
}

func (m *mockStorage) Range(
	ctx context.Context,
	opts ...storage.RangeOption,
) ([]kv.KeyValue, error) {
	return nil, nil
}

func (m *mockStorage) TxFactory() tx.Factory { return m.Tx }

func (m *mockStorage) NewLocker(context.Context, string, ...locker.Option) (locker.Locker, error) {
	return nil, errors.New("locking is not used by worker tests")
}

func (m *mockStorage) LockerFactory() locker.Factory { return m }
func TestWorkerPublish(t *testing.T) {
	workerCfg := `type: nontarantool
instrumentation:
  url: host1:8080
  metrics_url: /metrics
  metrics_format: prometheus
config:
  addr: host1:9080
`

	cases := []struct {
		name        string
		key         string
		src         []byte
		force       bool
		succeeded   bool
		txErr       error
		expectedErr string
	}{
		{
			name:      "new key without force",
			key:       "/prefix/instances/host1/worker1",
			src:       []byte(workerCfg),
			force:     false,
			succeeded: true,
		},
		{
			name:        "key exists without force",
			key:         "/prefix/instances/host1/worker1",
			src:         []byte(workerCfg),
			force:       false,
			succeeded:   false,
			expectedErr: "worker configuration already exists",
		},
		{
			name:  "key exists with force",
			key:   "/prefix/instances/host1/worker1",
			src:   []byte(workerCfg),
			force: true,
		},
		{
			name:  "new key with force",
			key:   "/prefix/instances/host1/worker1",
			src:   []byte(workerCfg),
			force: true,
		},
		{
			name:        "storage error",
			key:         "/prefix/instances/host1/worker1",
			src:         []byte(workerCfg),
			txErr:       errors.New("connection refused"),
			expectedErr: "failed to publish worker configuration",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mockTx := &mockTx{
				succeeded: testCase.succeeded,
				err:       testCase.txErr,
			}
			mockStg := &mockStorage{tx: mockTx}

			publishCtx := WorkerPublishCtx{
				Storage: mockStg,
				Key:     testCase.key,
				Src:     testCase.src,
				Force:   testCase.force,
			}

			err := WorkerPublish(publishCtx)

			if testCase.expectedErr != "" {
				require.ErrorContains(t, err, testCase.expectedErr)

				return
			}

			require.NoError(t, err)

			if testCase.force {
				require.Empty(t, mockTx.predicates)
			} else {
				require.Len(t, mockTx.predicates, 1)
				require.Equal(t, []byte(testCase.key), mockTx.predicates[0].Key())
				require.Equal(t, predicate.TargetVersion, mockTx.predicates[0].Target())
				require.Equal(t, int64(0), mockTx.predicates[0].Value())
				require.Equal(t, predicate.OpEqual, mockTx.predicates[0].Operation())
			}
		})
	}
}

func TestWorkerShow(t *testing.T) {
	workerCfg := `type: nontarantool
instrumentation:
  url: host1:8080
  metrics_url: /metrics
  metrics_format: prometheus
config:
  addr: host1:9080
`

	cases := []struct {
		name        string
		key         string
		value       []byte
		txErr       error
		expectedErr string
	}{
		{
			name:  "key found",
			key:   "/prefix/instances/host1/worker1",
			value: []byte(workerCfg),
		},
		{
			name:        "key not found",
			key:         "/prefix/instances/host1/worker1",
			value:       nil,
			expectedErr: "worker configuration not found",
		},
		{
			name:        "storage error",
			key:         "/prefix/instances/host1/worker1",
			txErr:       errors.New("connection refused"),
			expectedErr: "failed to get worker configuration",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var results []tx.RequestResponse

			if testCase.value != nil {
				results = []tx.RequestResponse{
					{
						Values: []kv.KeyValue{
							{Key: []byte(testCase.key), Value: testCase.value},
						},
					},
				}
			}

			mockStg := &mockStorage{
				tx: &mockTx{
					results: results,
					err:     testCase.txErr,
				},
			}

			showCtx := WorkerShowCtx{
				Storage: mockStg,
				Key:     testCase.key,
			}

			output, err := WorkerShow(showCtx)

			if testCase.expectedErr != "" {
				require.ErrorContains(t, err, testCase.expectedErr)

				return
			}

			require.NoError(t, err)
			require.Contains(t, string(output), "type: nontarantool")
			require.Contains(t, string(output), "host1:8080")
		})
	}
}

func TestWorkerDelete(t *testing.T) {
	workerCfg := `type: nontarantool
instrumentation:
  url: host1:8080
  metrics_url: /metrics
  metrics_format: prometheus
config:
  addr: host1:9080
`

	cases := []struct {
		name        string
		key         string
		value       []byte
		force       bool
		succeeded   bool
		txErr       error
		expectedErr string
	}{
		{
			name:      "key exists with force",
			key:       "/prefix/instances/host1/worker1",
			value:     []byte(workerCfg),
			force:     true,
			succeeded: true,
		},
		{
			name:      "key exists without force",
			key:       "/prefix/instances/host1/worker1",
			value:     []byte(workerCfg),
			force:     false,
			succeeded: true,
		},
		{
			name:        "key not found without force",
			key:         "/prefix/instances/host1/worker1",
			value:       nil,
			force:       false,
			succeeded:   false,
			expectedErr: "worker configuration not found",
		},
		{
			name:      "key not found with force succeeds",
			key:       "/prefix/instances/host1/worker1",
			value:     nil,
			force:     true,
			succeeded: true,
		},
		{
			name:        "storage error",
			key:         "/prefix/instances/host1/worker1",
			txErr:       errors.New("connection refused"),
			expectedErr: "failed to delete from storage",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var results []tx.RequestResponse

			if testCase.value != nil {
				results = []tx.RequestResponse{
					{
						Values: []kv.KeyValue{
							{Key: []byte(testCase.key), Value: testCase.value},
						},
					},
				}
			}

			mockTx := &mockTx{
				results:   results,
				succeeded: testCase.succeeded,
				err:       testCase.txErr,
			}
			mockStg := &mockStorage{tx: mockTx}

			deleteCtx := WorkerDeleteCtx{
				Storage: mockStg,
				Key:     testCase.key,
				Force:   testCase.force,
			}

			err := WorkerDelete(deleteCtx)

			if testCase.expectedErr != "" {
				require.ErrorContains(t, err, testCase.expectedErr)

				return
			}

			require.NoError(t, err)

			if testCase.force {
				require.Empty(t, mockTx.predicates)
			} else {
				require.Len(t, mockTx.predicates, 1)
				require.Equal(t, []byte(testCase.key), mockTx.predicates[0].Key())
				require.Equal(t, predicate.TargetVersion, mockTx.predicates[0].Target())
				require.Equal(t, int64(0), mockTx.predicates[0].Value())
				require.Equal(t, predicate.OpNotEqual, mockTx.predicates[0].Operation())
			}
		})
	}
}

func TestParseWorkerPathAndBuildKeyIntegration(t *testing.T) {
	cases := []struct {
		urlPath     string
		expectedKey string
	}{
		{
			urlPath:     "/tdb-workers/tdb-cluster/host1/http-server-1",
			expectedKey: "/tdb-workers/tdb-cluster/instances/host1/http-server-1",
		},
		{
			urlPath:     "/prefix/host/worker",
			expectedKey: "/prefix/instances/host/worker",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.urlPath, func(t *testing.T) {
			prefix, host, worker, err := ParseWorkerPath(testCase.urlPath)
			require.NoError(t, err)

			key := BuildWorkerStorageKey(prefix, host, worker)
			require.Equal(t, testCase.expectedKey, key)
		})
	}
}
