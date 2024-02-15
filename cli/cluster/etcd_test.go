package cluster_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/lib/integrity"
)

type MockEtcdGetter struct {
	cluster.EtcdGetter
	Kvs  []*mvccpb.KeyValue
	Err  error
	Ctx  context.Context
	Key  string
	Opts []clientv3.OpOption
}

func (g *MockEtcdGetter) Get(ctx context.Context, key string,
	opts ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	g.Ctx = ctx
	g.Key = key
	g.Opts = opts

	if g.Err != nil {
		return nil, g.Err
	}
	return &clientv3.GetResponse{
		Kvs: g.Kvs,
	}, nil
}

type MockTxn struct {
	IfCs    []clientv3.Cmp
	ThenOps []clientv3.Op
	ElseOps []clientv3.Op
	Resp    *clientv3.TxnResponse
	Err     error
}

func (txn *MockTxn) If(cs ...clientv3.Cmp) clientv3.Txn {
	txn.IfCs = cs
	return txn
}

func (txn *MockTxn) Then(ops ...clientv3.Op) clientv3.Txn {
	txn.ThenOps = ops
	return txn
}

func (txn *MockTxn) Else(ops ...clientv3.Op) clientv3.Txn {
	txn.ElseOps = ops
	return txn
}

func (txn *MockTxn) Commit() (*clientv3.TxnResponse, error) {
	return txn.Resp, txn.Err
}

type MockEtcdTxnGetter struct {
	MockEtcdGetter
	TxnRet *MockTxn
	CtxTxn context.Context
}

func (getter *MockEtcdTxnGetter) Txn(ctx context.Context) clientv3.Txn {
	getter.CtxTxn = ctx
	if getter.TxnRet == nil {
		getter.TxnRet = &MockTxn{Resp: &clientv3.TxnResponse{Succeeded: true}}
	}
	return getter.TxnRet
}

func TestClientKVImplementsEtcdGetter(t *testing.T) {
	var (
		kv     clientv3.KV
		getter cluster.EtcdGetter
	)
	getter = kv
	assert.Nil(t, getter)
}

func TestNewEtcdAllCollector(t *testing.T) {
	var collector integrity.DataCollector

	collector = cluster.NewEtcdAllCollector(&MockEtcdGetter{}, "", 0)

	assert.NotNil(t, collector)
}

func TestEtcdAllCollector_Collect_getter_inputs(t *testing.T) {
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
			mock := &MockEtcdGetter{
				Err: fmt.Errorf("any"),
			}
			cluster.NewEtcdAllCollector(mock, tc.Prefix, 0).Collect()

			assert.NotNil(t, mock.Ctx)
			assert.Equal(t, tc.Key, mock.Key)
			require.Len(t, mock.Opts, 1)
		})
	}
}

func TestEtcdCollectors_Collect_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}
	mock := &MockEtcdGetter{
		Err: fmt.Errorf("any"),
	}

	for _, tc := range cases {
		collectors := []struct {
			Name      string
			Collector integrity.DataCollector
		}{
			{"all", cluster.NewEtcdAllCollector(mock, "/foo", tc)},
			{"key", cluster.NewEtcdKeyCollector(mock, "/foo", "key", tc)},
		}
		for _, c := range collectors {
			t.Run(c.Name+fmt.Sprint(tc), func(t *testing.T) {
				c.Collector.Collect()

				expected := time.Now().Add(tc)
				deadline, ok := mock.Ctx.Deadline()
				if tc == 0 {
					assert.False(t, ok)
				} else {
					assert.True(t, ok)
					assert.InDelta(t, expected.Unix(), deadline.Unix(), 1)
				}
			})
		}
	}
}

func TestEtcdAllCollector_Collect_merge(t *testing.T) {
	cases := []struct {
		Kvs      []*mvccpb.KeyValue
		Expected []integrity.Data
	}{
		{
			Kvs: []*mvccpb.KeyValue{
				&mvccpb.KeyValue{
					Key:         []byte("k"),
					Value:       []byte("f: a\nb: a\n"),
					ModRevision: 1,
				},
			},
			Expected: []integrity.Data{{
				Source:   "k",
				Value:    []byte("f: a\nb: a\n"),
				Revision: 1,
			}},
		},
		{
			Kvs: []*mvccpb.KeyValue{
				&mvccpb.KeyValue{
					Key:         []byte("k"),
					Value:       []byte("f: a\nb: a\n"),
					ModRevision: 1,
				},
				&mvccpb.KeyValue{
					Key:         []byte("k"),
					Value:       []byte("f: b\nb: b\nc: b\n"),
					ModRevision: 2,
				},
			},
			Expected: []integrity.Data{
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
			mock := &MockEtcdGetter{
				Kvs: tc.Kvs,
			}
			config, err := cluster.NewEtcdAllCollector(mock, "foo", 0).Collect()

			assert.NoError(t, err)
			require.NotNil(t, config)
			assert.Equal(t, tc.Expected, config)
		})
	}
}

func TestEtcdCollectors_Collect_error(t *testing.T) {
	mock := &MockEtcdGetter{
		Err: fmt.Errorf("any"),
	}
	cases := []struct {
		Name      string
		Collector integrity.DataCollector
	}{
		{"all", cluster.NewEtcdAllCollector(mock, "/foo", 0)},
		{"key", cluster.NewEtcdKeyCollector(mock, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()

			assert.ErrorContains(t, err, "failed to fetch data from etcd: any")
			assert.Nil(t, config)
		})
	}
}

func TestEtcdCollectors_Collect_empty(t *testing.T) {
	mock := &MockEtcdGetter{
		Kvs: nil,
	}
	cases := []struct {
		Name      string
		Collector integrity.DataCollector
	}{
		{"all", cluster.NewEtcdAllCollector(mock, "/foo", 0)},
		{"key", cluster.NewEtcdKeyCollector(mock, "/foo", "key", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			config, err := tc.Collector.Collect()
			assert.Error(t, err)
			assert.Nil(t, config)
		})
	}
}

func TestNewEtcdKeyCollector(t *testing.T) {
	var collector integrity.DataCollector

	collector = cluster.NewEtcdKeyCollector(&MockEtcdGetter{}, "", "", 0)

	assert.NotNil(t, collector)
}

func TestEtcdKeyCollector_Collect_getter_inputs(t *testing.T) {
	cases := []struct {
		Prefix   string
		Key      string
		Expected string
	}{
		{"", "", "/config/"},
		{"////", "//", "/config///"},
		{"foo", "foo", "foo/config/foo"},
		{"/foo/bar", "/foo", "/foo/bar/config//foo"},
		{"/foo/bar////", "//foo//", "/foo/bar/config///foo//"},
	}
	for _, tc := range cases {
		t.Run(tc.Expected, func(t *testing.T) {
			mock := &MockEtcdGetter{
				Err: fmt.Errorf("any"),
			}
			cluster.NewEtcdKeyCollector(mock, tc.Prefix, tc.Key, 0).Collect()

			assert.NotNil(t, mock.Ctx)
			assert.Equal(t, tc.Expected, mock.Key)
			require.Len(t, mock.Opts, 0)
		})
	}
}

func TestEtcdKeyCollector_Collect_key(t *testing.T) {
	mock := &MockEtcdGetter{
		Kvs: []*mvccpb.KeyValue{
			&mvccpb.KeyValue{
				Key:         []byte("k"),
				Value:       []byte("f: a\nb: a\n"),
				ModRevision: 1,
			},
		},
	}
	expected := []integrity.Data{{
		Source:   "k",
		Value:    []byte("f: a\nb: a\n"),
		Revision: 1,
	}}

	config, err := cluster.NewEtcdKeyCollector(mock, "foo", "key", 0).Collect()

	assert.NoError(t, err)
	require.NotNil(t, config)
	assert.Equal(t, expected, config)
}

func TestEtcdKeyCollector_Collect_too_many(t *testing.T) {
	mock := &MockEtcdGetter{
		Kvs: []*mvccpb.KeyValue{
			&mvccpb.KeyValue{
				Key:         []byte("k"),
				Value:       []byte("f: a\nb: a\n"),
				ModRevision: 1,
			},
			&mvccpb.KeyValue{
				Key:         []byte("k"),
				Value:       []byte("f: b\nb: b\nc: b\n"),
				ModRevision: 2,
			},
		},
	}

	config, err := cluster.NewEtcdKeyCollector(mock, "foo", "key", 0).Collect()

	assert.ErrorContains(t, err, "too many responses")
	require.Nil(t, config)
}

func TestNewEtcdAllDataPublisher(t *testing.T) {
	var publisher integrity.DataPublisher

	publisher = cluster.NewEtcdAllDataPublisher(nil, "", 0)

	assert.NotNil(t, publisher)
}

func TestEtcdAllDataPublisher_Publish_get_inputs(t *testing.T) {
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
			mock := &MockEtcdTxnGetter{}
			cluster.NewEtcdAllDataPublisher(mock, tc.Prefix, 0).Publish(0, data)

			assert.NotNil(t, mock.Ctx)
			assert.Equal(t, tc.Key, mock.Key)
			require.Len(t, mock.Opts, 1)
		})
	}
}

func TestEtcdAllDataPublisher_Publish_txn_inputs(t *testing.T) {
	cases := []struct {
		Name    string
		Mock    *MockEtcdTxnGetter
		IfLen   int
		ThenLen int
	}{
		{
			Name:    "no get keys",
			Mock:    &MockEtcdTxnGetter{},
			IfLen:   0,
			ThenLen: 1,
		},
		{
			Name: "get keys",
			Mock: &MockEtcdTxnGetter{
				MockEtcdGetter: MockEtcdGetter{
					Kvs: []*mvccpb.KeyValue{
						&mvccpb.KeyValue{
							Key: []byte("foo"),
						},
						&mvccpb.KeyValue{
							Key: []byte("foo"),
						},
						&mvccpb.KeyValue{
							Key: []byte("foo"),
						},
					},
				},
			},
			IfLen:   3,
			ThenLen: 4,
		},
		{
			Name: "get keys with target",
			Mock: &MockEtcdTxnGetter{
				MockEtcdGetter: MockEtcdGetter{
					Kvs: []*mvccpb.KeyValue{
						&mvccpb.KeyValue{
							Key: []byte("foo"),
						},
						&mvccpb.KeyValue{
							Key: []byte("foo"),
						},
						&mvccpb.KeyValue{
							Key: []byte("/foo/config/all"),
						},
					},
				},
			},
			IfLen:   2,
			ThenLen: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			publisher := cluster.NewEtcdAllDataPublisher(tc.Mock, "/foo", 0)
			publisher.Publish(0, []byte{})

			assert.Len(t, tc.Mock.TxnRet.IfCs, tc.IfLen)
			assert.Len(t, tc.Mock.TxnRet.ThenOps, tc.ThenLen)
			assert.Len(t, tc.Mock.TxnRet.ElseOps, 0)

			// Cs and Ops structures have not any public fields. So
			// we can't check it directly and we need additional integration
			// tests.
			for i, op := range tc.Mock.TxnRet.ThenOps {
				if i == len(tc.Mock.TxnRet.ThenOps)-1 {
					assert.True(t, op.IsPut())
				} else {
					assert.True(t, op.IsDelete())
				}
			}
		})
	}
}

func TestEtcdDataPublishers_Publish_data_nil(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher integrity.DataPublisher
	}{
		{"all", cluster.NewEtcdAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewEtcdKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := tc.Publisher.Publish(0, nil)

			assert.EqualError(t, err,
				"failed to publish data into etcd: data does not exist")
		})
	}
}

func TestEtcdDataPublishers_Publish_publisher_nil(t *testing.T) {
	cases := []struct {
		Name      string
		Publisher integrity.DataPublisher
	}{
		{"all", cluster.NewEtcdAllDataPublisher(nil, "", 0)},
		{"key", cluster.NewEtcdKeyDataPublisher(nil, "", "", 0)},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			assert.Panics(t, func() {
				tc.Publisher.Publish(0, []byte{})
			})
		})
	}
}

func TestEtcdAllDataPublisher_Publish_errors(t *testing.T) {
	cases := []struct {
		Name     string
		Mock     cluster.EtcdTxnGetter
		Expected string
	}{
		{
			Name:     "no error",
			Mock:     &MockEtcdTxnGetter{},
			Expected: "",
		},
		{
			Name: "get error",
			Mock: &MockEtcdTxnGetter{
				MockEtcdGetter: MockEtcdGetter{
					Err: fmt.Errorf("get"),
				},
			},
			Expected: "failed to fetch data from etcd: get",
		},
		{
			Name: "txn commit error",
			Mock: &MockEtcdTxnGetter{
				TxnRet: &MockTxn{Err: fmt.Errorf("txn commit")},
			},
			Expected: "failed to put data into etcd: txn commit",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			publisher := cluster.NewEtcdAllDataPublisher(tc.Mock, "prefix", 0)
			err := publisher.Publish(0, []byte{})
			if tc.Expected != "" {
				assert.EqualError(t, err, tc.Expected)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEtcdAllDataPublisher_Publish_revision(t *testing.T) {
	mock := &MockEtcdTxnGetter{}
	publisher := cluster.NewEtcdAllDataPublisher(mock, "prefix", 0)
	err := publisher.Publish(1, []byte{})
	assert.EqualError(t, err,
		"failed to publish data into etcd: target revision 1 is not supported")
}

func TestEtcdAllDataPublisher_Publish_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			mock := &MockEtcdTxnGetter{}
			publisher := cluster.NewEtcdAllDataPublisher(mock, "prefix", tc)
			err := publisher.Publish(0, []byte{})

			require.NoError(t, err)
			require.NotNil(t, mock.Ctx)
			require.NotNil(t, mock.CtxTxn)
			assert.Equal(t, mock.Ctx, mock.CtxTxn)

			if tc == 0 {
				_, ok := mock.Ctx.Deadline()
				assert.False(t, ok)
				_, ok = mock.CtxTxn.Deadline()
				assert.False(t, ok)
			} else {
				expected := time.Now().Add(tc)
				deadline, ok := mock.Ctx.Deadline()
				assert.True(t, ok)
				assert.InDelta(t, expected.Unix(), deadline.Unix(), 1)
				deadline, ok = mock.CtxTxn.Deadline()
				assert.True(t, ok)
				assert.InDelta(t, expected.Unix(), deadline.Unix(), 1)
			}
		})
	}
}

func TestEtcdAllDataPublisher_Publish_timeout_exit(t *testing.T) {
	mock := &MockEtcdTxnGetter{
		TxnRet: &MockTxn{
			Resp: &clientv3.TxnResponse{Succeeded: false},
		},
	}

	// You should increase the values if the test is flaky.
	before := time.Now()
	timeout := 100 * time.Millisecond
	delta := 30 * time.Millisecond
	publisher := cluster.NewEtcdAllDataPublisher(mock, "prefix", timeout)
	err := publisher.Publish(0, []byte{})
	assert.EqualError(t, err, "context deadline exceeded")
	assert.InDelta(t, timeout, time.Since(before), float64(delta))
}

func TestNewEtcdKeyDataPublisher(t *testing.T) {
	var publisher integrity.DataPublisher

	publisher = cluster.NewEtcdKeyDataPublisher(nil, "", "", 0)

	assert.NotNil(t, publisher)
}

func TestEtcdKeyDataPublisher_Publish_inputs(t *testing.T) {
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
			mock := &MockEtcdTxnGetter{TxnRet: &MockTxn{Err: fmt.Errorf("foo")}}
			publisher := cluster.NewEtcdKeyDataPublisher(mock, tc.Prefix, tc.Key, 0)
			publisher.Publish(0, data)

			assert.NotNil(t, mock.CtxTxn)
			assert.Equal(t,
				[]clientv3.Op{clientv3.OpPut(tc.Expected, string(data))}, mock.TxnRet.ThenOps)
			assert.Nil(t, mock.TxnRet.IfCs)
			assert.Nil(t, mock.TxnRet.ElseOps)
		})
	}
}

func TestEtcdKeyDataPublisher_Publish_modRevision(t *testing.T) {
	prefix := "/foo"
	key := "key"
	modRevision := int64(5)
	data := []byte("foo bar")
	expected := "/foo/config/key"
	mock := &MockEtcdTxnGetter{TxnRet: &MockTxn{Err: fmt.Errorf("foo")}}
	publisher := cluster.NewEtcdKeyDataPublisher(mock, prefix, key, 0)

	publisher.Publish(modRevision, data)
	assert.NotNil(t, mock.CtxTxn)
	assert.Equal(t,
		[]clientv3.Op{clientv3.OpPut(expected, string(data))},
		mock.TxnRet.ThenOps)
	assert.Equal(t,
		[]clientv3.Cmp{clientv3.Compare(clientv3.ModRevision(expected), "=", modRevision)},
		mock.TxnRet.IfCs)
	assert.Nil(t, mock.TxnRet.ElseOps)
}

func TestEtcdKeyDataPublisher_Publish_error(t *testing.T) {
	mock := &MockEtcdTxnGetter{TxnRet: &MockTxn{Err: fmt.Errorf("foo")}}
	publisher := cluster.NewEtcdKeyDataPublisher(mock, "", "", 0)
	err := publisher.Publish(0, []byte{})

	assert.EqualError(t, err, "failed to put data into etcd: foo")
}

func TestEtcdKeyDataPublisher_Publish_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}
	mock := &MockEtcdTxnGetter{TxnRet: &MockTxn{Err: fmt.Errorf("foo")}}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			publisher := cluster.NewEtcdKeyDataPublisher(mock, "", "", tc)
			publisher.Publish(0, []byte{})

			expected := time.Now().Add(tc)
			deadline, ok := mock.CtxTxn.Deadline()
			if tc == 0 {
				assert.False(t, ok)
			} else {
				assert.True(t, ok)
				assert.InDelta(t, expected.Unix(), deadline.Unix(), 1)
			}
		})
	}
}
