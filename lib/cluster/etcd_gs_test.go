package cluster_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-storage"
	"github.com/tarantool/go-storage/kv"
	"github.com/tarantool/go-storage/operation"
	"github.com/tarantool/go-storage/predicate"
	"github.com/tarantool/go-storage/tx"
	"github.com/tarantool/go-storage/watch"

	"github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
)

type MockGSDriver struct {
	Ctxs            []context.Context
	PredicatesCalls [][]predicate.Predicate
	ThenOpsCalls    [][]operation.Operation
	ElseOpsCalls    [][]operation.Operation
	Responses       []tx.Response
	Errors          []error
	ExecuteFunc     func(
		ctx context.Context,
		predicates []predicate.Predicate,
		thenOps []operation.Operation,
		elseOps []operation.Operation,
	) (tx.Response, error)
}

func (d *MockGSDriver) Execute(
	ctx context.Context,
	predicates []predicate.Predicate,
	thenOps []operation.Operation,
	elseOps []operation.Operation,
) (tx.Response, error) {
	d.Ctxs = append(d.Ctxs, ctx)
	d.PredicatesCalls = append(d.PredicatesCalls, predicates)
	d.ThenOpsCalls = append(d.ThenOpsCalls, thenOps)
	d.ElseOpsCalls = append(d.ElseOpsCalls, elseOps)

	if d.ExecuteFunc != nil {
		return d.ExecuteFunc(ctx, predicates, thenOps, elseOps)
	}

	callID := len(d.Ctxs) - 1
	resp := tx.Response{Succeeded: true}
	if callID < len(d.Responses) {
		resp = d.Responses[callID]
	}

	var err error
	if callID < len(d.Errors) {
		err = d.Errors[callID]
	}

	return resp, err
}

func (d *MockGSDriver) Tx(ctx context.Context) tx.Tx {
	return storage.NewStorage(mockGSDriverAdapter{driver: d}).Tx(ctx)
}

func (d *MockGSDriver) Range(
	ctx context.Context,
	opts ...storage.RangeOption,
) ([]kv.KeyValue, error) {
	return storage.NewStorage(mockGSDriverAdapter{driver: d}).Range(ctx, opts...)
}

func (d *MockGSDriver) Watch(
	context.Context,
	[]byte,
	...watch.Option,
) <-chan watch.Event {
	ch := make(chan watch.Event)
	close(ch)

	return ch
}

type mockGSDriverAdapter struct {
	driver *MockGSDriver
}

func (a mockGSDriverAdapter) Execute(
	ctx context.Context,
	predicates []predicate.Predicate,
	thenOps []operation.Operation,
	elseOps []operation.Operation,
) (tx.Response, error) {
	return a.driver.Execute(ctx, predicates, thenOps, elseOps)
}

func (a mockGSDriverAdapter) Watch(
	context.Context,
	[]byte,
	...watch.Option,
) (<-chan watch.Event, func(), error) {
	return nil, func() {}, nil
}

func newGetResponse(values []kv.KeyValue) tx.Response {
	return tx.Response{
		Succeeded: true,
		Results: []tx.RequestResponse{
			{Values: values},
		},
	}
}

func assertDeadline(t *testing.T, ctx context.Context, timeout time.Duration) {
	t.Helper()

	expected := time.Now().Add(timeout)
	deadline, ok := ctx.Deadline()
	if timeout == 0 {
		assert.False(t, ok)
		return
	}

	assert.True(t, ok)
	assert.InDelta(t, expected.Unix(), deadline.Unix(), 1)
}

func TestNewGSEtcdAllCollector(t *testing.T) {
	var collector cluster.DataCollector

	collector = cluster.NewGSEtcdAllCollector(&MockGSDriver{}, "", 0)

	assert.NotNil(t, collector)
}

func TestGSEtcdAllCollector_Collect_driver_inputs(t *testing.T) {
	cases := []struct {
		Prefix string
		Key    string
	}{
		{"", "/config/"},
		{"////", "/config/"},
		{"foo", "foo/config/"},
		{"/foo/bar", "/foo/bar/config/"},
		{"/foo/bar////", "/foo/bar/config/"},
	}
	for _, tc := range cases {
		t.Run(tc.Prefix, func(t *testing.T) {
			mock := &MockGSDriver{
				Errors: []error{fmt.Errorf("any")},
			}
			cluster.NewGSEtcdAllCollector(mock, tc.Prefix, 0).Collect()

			require.Len(t, mock.Ctxs, 1)
			require.Len(t, mock.ThenOpsCalls, 1)
			require.Empty(t, mock.PredicatesCalls[0])
			require.Empty(t, mock.ElseOpsCalls[0])
			require.Len(t, mock.ThenOpsCalls[0], 1)

			op := mock.ThenOpsCalls[0][0]
			assert.Equal(t, operation.TypeGet, op.Type())
			assert.Equal(t, []byte(tc.Key), op.Key())
			assert.True(t, op.IsPrefix())
		})
	}
}

func TestGSEtcdCollectors_Collect_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}

	for _, tc := range cases {
		collectors := []struct {
			Name      string
			Collector cluster.DataCollector
			Mock      *MockGSDriver
		}{
			{
				Name:      "all",
				Collector: cluster.NewGSEtcdAllCollector(&MockGSDriver{Errors: []error{fmt.Errorf("any")}}, "/foo", tc),
				Mock:      &MockGSDriver{Errors: []error{fmt.Errorf("any")}},
			},
			{
				Name:      "key",
				Collector: cluster.NewGSEtcdKeyCollector(&MockGSDriver{Errors: []error{fmt.Errorf("any")}}, "/foo", "key", tc),
				Mock:      &MockGSDriver{Errors: []error{fmt.Errorf("any")}},
			},
		}
		for i := range collectors {
			collectors[i].Collector = map[string]cluster.DataCollector{
				"all": cluster.NewGSEtcdAllCollector(collectors[i].Mock, "/foo", tc),
				"key": cluster.NewGSEtcdKeyCollector(collectors[i].Mock, "/foo", "key", tc),
			}[collectors[i].Name]
		}
		for _, c := range collectors {
			t.Run(c.Name+fmt.Sprint(tc), func(t *testing.T) {
				c.Collector.Collect()

				require.Len(t, c.Mock.Ctxs, 1)
				assertDeadline(t, c.Mock.Ctxs[0], tc)
			})
		}
	}
}

func TestGSEtcdAllCollector_Collect_merge(t *testing.T) {
	cases := []struct {
		Kvs      []kv.KeyValue
		Expected []cluster.Data
	}{
		{
			Kvs: []kv.KeyValue{
				{
					Key:         []byte("k"),
					Value:       []byte("f: a\nb: a\n"),
					ModRevision: 1,
				},
			},
			Expected: []cluster.Data{{
				Source:   "k",
				Value:    []byte("f: a\nb: a\n"),
				Revision: 1,
			}},
		},
		{
			Kvs: []kv.KeyValue{
				{
					Key:         []byte("k"),
					Value:       []byte("f: a\nb: a\n"),
					ModRevision: 1,
				},
				{
					Key:         []byte("k"),
					Value:       []byte("f: b\nb: b\nc: b\n"),
					ModRevision: 2,
				},
			},
			Expected: []cluster.Data{
				{
					Source:   "k",
					Value:    []byte("f: a\nb: a\n"),
					Revision: 1,
				},
				{
					Source:   "k",
					Value:    []byte("f: b\nb: b\nc: b\n"),
					Revision: 2,
				},
			},
		},
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			mock := &MockGSDriver{
				Responses: []tx.Response{newGetResponse(tc.Kvs)},
			}
			config, err := cluster.NewGSEtcdAllCollector(mock, "foo", 0).Collect()

			assert.NoError(t, err)
			require.NotNil(t, config)
			assert.Equal(t, tc.Expected, config)
		})
	}
}

func TestGSEtcdCollectors_Collect_error(t *testing.T) {
	cases := []struct {
		Name      string
		Collector cluster.DataCollector
	}{
		{"all", cluster.NewGSEtcdAllCollector(&MockGSDriver{Errors: []error{fmt.Errorf("any")}}, "/foo", 0)},
		{"key", cluster.NewGSEtcdKeyCollector(&MockGSDriver{Errors: []error{fmt.Errorf("any")}}, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()

			assert.ErrorContains(t, err, "failed to fetch data from etcd:")
			assert.ErrorContains(t, err, "any")
			assert.Nil(t, config)
		})
	}
}

func TestGSEtcdCollectors_Collect_empty(t *testing.T) {
	mock := &MockGSDriver{
		Responses: []tx.Response{newGetResponse(nil)},
	}
	cases := []struct {
		Name      string
		Collector cluster.DataCollector
	}{
		{"all", cluster.NewGSEtcdAllCollector(mock, "/foo", 0)},
		{"key", cluster.NewGSEtcdKeyCollector(mock, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()
			assert.Error(t, err)
			assert.Nil(t, config)
		})
	}
}

func TestNewGSEtcdKeyCollector(t *testing.T) {
	var collector cluster.DataCollector

	collector = cluster.NewGSEtcdKeyCollector(&MockGSDriver{}, "", "", 0)

	assert.NotNil(t, collector)
}

func TestGSEtcdKeyCollector_Collect_driver_inputs(t *testing.T) {
	cases := []struct {
		Prefix   string
		Key      string
		Expected string
		IsPrefix bool
	}{
		{"", "", "/config/", true},
		{"////", "//", "/config///", true},
		{"foo", "foo", "foo/config/foo", false},
		{"/foo/bar", "/foo", "/foo/bar/config//foo", false},
		{"/foo/bar////", "//foo//", "/foo/bar/config///foo//", true},
	}
	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			mock := &MockGSDriver{
				Errors: []error{fmt.Errorf("any")},
			}
			cluster.NewGSEtcdKeyCollector(mock, tc.Prefix, tc.Key, 0).Collect()

			require.Len(t, mock.Ctxs, 1)
			require.Len(t, mock.ThenOpsCalls, 1)
			require.Empty(t, mock.PredicatesCalls[0])
			require.Empty(t, mock.ElseOpsCalls[0])
			require.Len(t, mock.ThenOpsCalls[0], 1)

			op := mock.ThenOpsCalls[0][0]
			assert.Equal(t, operation.TypeGet, op.Type())
			assert.Equal(t, []byte(tc.Expected), op.Key())
			assert.Equal(t, tc.IsPrefix, op.IsPrefix())
		})
	}
}

func TestGSEtcdKeyCollector_Collect_key(t *testing.T) {
	mock := &MockGSDriver{
		Responses: []tx.Response{newGetResponse([]kv.KeyValue{
			{
				Key:         []byte("k"),
				Value:       []byte("f: a\nb: a\n"),
				ModRevision: 1,
			},
		})},
	}
	expected := []cluster.Data{{
		Source:   "k",
		Value:    []byte("f: a\nb: a\n"),
		Revision: 1,
	}}

	config, err := cluster.NewGSEtcdKeyCollector(mock, "foo", "key", 0).Collect()

	assert.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, expected, config)
}

func TestGSEtcdKeyCollector_Collect_too_many(t *testing.T) {
	mock := &MockGSDriver{
		Responses: []tx.Response{newGetResponse([]kv.KeyValue{
			{
				Key:         []byte("k"),
				Value:       []byte("f: a\nb: a\n"),
				ModRevision: 1,
			},
			{
				Key:         []byte("k"),
				Value:       []byte("f: b\nb: b\nc: b\n"),
				ModRevision: 2,
			},
		})},
	}

	config, err := cluster.NewGSEtcdKeyCollector(mock, "foo", "key", 0).Collect()

	assert.ErrorContains(t, err, "too many responses")
	require.Nil(t, config)
}

func TestNewGSEtcdAllDataPublisher(t *testing.T) {
	var publisher cluster.DataPublisher

	publisher = cluster.NewGSEtcdAllDataPublisher(nil, "", 0)

	assert.NotNil(t, publisher)
}

func TestGSEtcdAllDataPublisher_Publish_get_inputs(t *testing.T) {
	cases := []struct {
		Prefix string
		Key    string
	}{
		{"", "/config/"},
		{"////", "/config/"},
		{"foo", "foo/config/"},
		{"/foo/bar", "/foo/bar/config/"},
		{"/foo/bar////", "/foo/bar/config/"},
	}
	data := []byte("foo bar")

	for _, tc := range cases {
		t.Run(tc.Prefix, func(t *testing.T) {
			mock := &MockGSDriver{}
			cluster.NewGSEtcdAllDataPublisher(mock, tc.Prefix, 0).Publish(0, data)

			require.Len(t, mock.Ctxs, 2)
			require.Len(t, mock.ThenOpsCalls, 2)
			require.Len(t, mock.ThenOpsCalls[0], 1)
			assert.Equal(t, operation.TypeGet, mock.ThenOpsCalls[0][0].Type())
			assert.Equal(t, []byte(tc.Key), mock.ThenOpsCalls[0][0].Key())
			assert.True(t, mock.ThenOpsCalls[0][0].IsPrefix())
		})
	}
}

func TestGSEtcdAllDataPublisher_Publish_txn_inputs(t *testing.T) {
	cases := []struct {
		Name    string
		Mock    *MockGSDriver
		IfLen   int
		ThenLen int
	}{
		{
			Name:    "no get keys",
			Mock:    &MockGSDriver{},
			IfLen:   0,
			ThenLen: 1,
		},
		{
			Name: "get keys",
			Mock: &MockGSDriver{
				Responses: []tx.Response{newGetResponse([]kv.KeyValue{
					{Key: []byte("foo"), ModRevision: 1},
					{Key: []byte("bar"), ModRevision: 2},
					{Key: []byte("baz"), ModRevision: 3},
				})},
			},
			IfLen:   3,
			ThenLen: 4,
		},
		{
			Name: "get keys with target",
			Mock: &MockGSDriver{
				Responses: []tx.Response{newGetResponse([]kv.KeyValue{
					{Key: []byte("foo"), ModRevision: 1},
					{Key: []byte("bar"), ModRevision: 2},
					{Key: []byte("/foo/config/all"), ModRevision: 3},
				})},
			},
			IfLen:   2,
			ThenLen: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			publisher := cluster.NewGSEtcdAllDataPublisher(tc.Mock, "/foo", 0)
			err := publisher.Publish(0, []byte{})

			require.NoError(t, err)
			require.Len(t, tc.Mock.PredicatesCalls, 2)
			require.Len(t, tc.Mock.ThenOpsCalls, 2)
			assert.Len(t, tc.Mock.PredicatesCalls[1], tc.IfLen)
			assert.Len(t, tc.Mock.ThenOpsCalls[1], tc.ThenLen)
			assert.Len(t, tc.Mock.ElseOpsCalls[1], 0)

			for i, op := range tc.Mock.ThenOpsCalls[1] {
				if i == len(tc.Mock.ThenOpsCalls[1])-1 {
					assert.Equal(t, operation.TypePut, op.Type())
					assert.Equal(t, []byte("/foo/config/all"), op.Key())
				} else {
					assert.Equal(t, operation.TypeDelete, op.Type())
				}
			}
		})
	}
}

func TestGSEtcdDataPublishers_Publish_data_nil(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewGSEtcdAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewGSEtcdKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := tc.Publisher.Publish(0, nil)

			assert.EqualError(t, err,
				"failed to publish data into etcd: data does not exist")
		})
	}
}

func TestGSEtcdDataPublishers_Publish_publisher_nil(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher cluster.DataPublisher
	}{
		{"all", cluster.NewGSEtcdAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewGSEtcdKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Publisher.Publish(0, []byte{})
			})
		})
	}
}

func TestGSEtcdAllDataPublisher_Publish_errors(t *testing.T) {
	cases := []struct {
		Name     string
		Mock     storage.Storage
		Expected string
	}{
		{
			Name:     "no error",
			Mock:     &MockGSDriver{},
			Expected: "",
		},
		{
			Name: "get error",
			Mock: &MockGSDriver{
				Errors: []error{fmt.Errorf("get")},
			},
			Expected: "failed to fetch data from etcd: failed to execute ops: get",
		},
		{
			Name: "execute error",
			Mock: &MockGSDriver{
				ExecuteFunc: func(
					_ context.Context,
					_ []predicate.Predicate,
					thenOps []operation.Operation,
					_ []operation.Operation,
				) (tx.Response, error) {
					if len(thenOps) == 1 && thenOps[0].Type() == operation.TypeGet {
						return newGetResponse(nil), nil
					}

					return tx.Response{}, fmt.Errorf("execute")
				},
			},
			Expected: "failed to put data into etcd: tx execute failed: execute",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			publisher := cluster.NewGSEtcdAllDataPublisher(tc.Mock, "prefix", 0)
			err := publisher.Publish(0, []byte{})
			if tc.Expected != "" {
				assert.EqualError(t, err, tc.Expected)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGSEtcdAllDataPublisher_Publish_revision(t *testing.T) {
	mock := &MockGSDriver{}
	publisher := cluster.NewGSEtcdAllDataPublisher(mock, "prefix", 0)
	err := publisher.Publish(1, []byte{})
	assert.EqualError(t, err,
		"failed to publish data into etcd: target revision 1 is not supported")
}

func TestGSEtcdAllDataPublisher_Publish_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			mock := &MockGSDriver{}
			publisher := cluster.NewGSEtcdAllDataPublisher(mock, "prefix", tc)
			err := publisher.Publish(0, []byte{})

			require.NoError(t, err)
			require.Len(t, mock.Ctxs, 2)
			assert.Equal(t, mock.Ctxs[0], mock.Ctxs[1])
			assertDeadline(t, mock.Ctxs[0], tc)
			assertDeadline(t, mock.Ctxs[1], tc)
		})
	}
}

func TestGSEtcdAllDataPublisher_Publish_timeout_exit(t *testing.T) {
	mock := &MockGSDriver{
		ExecuteFunc: func(
			_ context.Context,
			_ []predicate.Predicate,
			thenOps []operation.Operation,
			_ []operation.Operation,
		) (tx.Response, error) {
			if len(thenOps) == 1 && thenOps[0].Type() == operation.TypeGet {
				return newGetResponse(nil), nil
			}

			return tx.Response{Succeeded: false}, nil
		},
	}

	before := time.Now()
	timeout := 100 * time.Millisecond
	delta := 30 * time.Millisecond
	publisher := cluster.NewGSEtcdAllDataPublisher(mock, "prefix", timeout)
	err := publisher.Publish(0, []byte{})
	assert.EqualError(t, err, "context deadline exceeded")
	assert.InDelta(t, timeout, time.Since(before), float64(delta))
}

func TestNewGSEtcdKeyDataPublisher(t *testing.T) {
	var publisher cluster.DataPublisher

	publisher = cluster.NewGSEtcdKeyDataPublisher(nil, "", "", 0)

	assert.NotNil(t, publisher)
}

func TestGSEtcdKeyDataPublisher_Publish_inputs(t *testing.T) {
	cases := []struct {
		Prefix   string
		Key      string
		Expected string
	}{
		{"", "foo", "/config/foo"},
		{"////", "foo", "/config/foo"},
		{"foo", "foo", "foo/config/foo"},
		{"/foo/bar", "foo", "/foo/bar/config/foo"},
		{"/foo/bar////", "foo", "/foo/bar/config/foo"},
		{"/foo/bar////", "", "/foo/bar/config/"},
		{"/foo/bar////", "//foo//", "/foo/bar/config///foo//"},
	}
	data := []byte("foo bar")

	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			mock := &MockGSDriver{}
			publisher := cluster.NewGSEtcdKeyDataPublisher(mock, tc.Prefix, tc.Key, 0)
			err := publisher.Publish(0, data)

			require.NoError(t, err)
			require.Len(t, mock.Ctxs, 1)
			require.Len(t, mock.ThenOpsCalls, 1)
			assert.Equal(t,
				[]operation.Operation{operation.Put([]byte(tc.Expected), data)},
				mock.ThenOpsCalls[0])
			assert.Nil(t, mock.PredicatesCalls[0])
			assert.Nil(t, mock.ElseOpsCalls[0])
		})
	}
}

func TestGSEtcdKeyDataPublisher_Publish_modRevision(t *testing.T) {
	prefix := "/foo"
	key := "key"
	modRevision := int64(5)
	data := []byte("foo bar")
	expected := "/foo/config/key"
	mock := &MockGSDriver{}
	publisher := cluster.NewGSEtcdKeyDataPublisher(mock, prefix, key, 0)

	err := publisher.Publish(modRevision, data)
	require.NoError(t, err)
	require.Len(t, mock.Ctxs, 1)
	assert.Equal(t,
		[]operation.Operation{operation.Put([]byte(expected), data)},
		mock.ThenOpsCalls[0])
	require.Len(t, mock.PredicatesCalls[0], 1)
	assert.Equal(t, []byte(expected), mock.PredicatesCalls[0][0].Key())
	assert.Equal(t, predicate.OpEqual, mock.PredicatesCalls[0][0].Operation())
	assert.Equal(t, predicate.TargetVersion, mock.PredicatesCalls[0][0].Target())
	assert.Equal(t, modRevision, mock.PredicatesCalls[0][0].Value())
	assert.Nil(t, mock.ElseOpsCalls[0])
}

func TestGSEtcdKeyDataPublisher_Publish_error(t *testing.T) {
	mock := &MockGSDriver{
		Errors: []error{fmt.Errorf("foo")},
	}
	publisher := cluster.NewGSEtcdKeyDataPublisher(mock, "", "", 0)
	err := publisher.Publish(0, []byte{})

	assert.EqualError(t, err, "failed to put data into etcd: tx execute failed: foo")
}

func TestGSEtcdKeyDataPublisher_Publish_wrong_revision(t *testing.T) {
	mock := &MockGSDriver{
		Responses: []tx.Response{{Succeeded: false}},
	}
	publisher := cluster.NewGSEtcdKeyDataPublisher(mock, "", "", 0)
	err := publisher.Publish(1, []byte{})

	assert.EqualError(t, err, "failed to put data into etcd: wrong revision")
}

func TestGSEtcdKeyDataPublisher_Publish_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}
	mock := &MockGSDriver{
		Errors: []error{fmt.Errorf("foo")},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			mock.Ctxs = nil
			mock.PredicatesCalls = nil
			mock.ThenOpsCalls = nil
			mock.ElseOpsCalls = nil

			publisher := cluster.NewGSEtcdKeyDataPublisher(mock, "", "", tc)
			publisher.Publish(0, []byte{})

			require.Len(t, mock.Ctxs, 1)
			assertDeadline(t, mock.Ctxs[0], tc)
		})
	}
}

func TestGSMakeEtcdOptsFromUriOpts(t *testing.T) {
	cases := []struct {
		Name     string
		UriOpts  connect.UriOpts
		Expected cluster.EtcdOpts
	}{
		{
			Name:     "empty",
			UriOpts:  connect.UriOpts{},
			Expected: cluster.EtcdOpts{},
		},
		{
			Name: "ignored",
			UriOpts: connect.UriOpts{
				Host:   "foo",
				Prefix: "foo",
				Params: map[string]string{
					"key":  "bar",
					"name": "zoo",
				},
				Ciphers: "foo:bar:ciphers",
			},
			Expected: cluster.EtcdOpts{},
		},
		{
			Name: "skip_host_verify",
			UriOpts: connect.UriOpts{
				SkipHostVerify: true,
			},
			Expected: cluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "skip_peer_verify",
			UriOpts: connect.UriOpts{
				SkipPeerVerify: true,
			},
			Expected: cluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "full",
			UriOpts: connect.UriOpts{
				Endpoint: "foo",
				Host:     "host",
				Prefix:   "prefix",
				Params: map[string]string{
					"key":  "key",
					"name": "instance",
				},
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        2 * time.Second,
			},
			Expected: cluster.EtcdOpts{
				Endpoints:      []string{"foo"},
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				SkipHostVerify: true,
				Timeout:        2 * time.Second,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			etcdOpts := cluster.MakeEtcdOptsFromUriOpts(tc.UriOpts)

			assert.Equal(t, tc.Expected, etcdOpts)
		})
	}
}
