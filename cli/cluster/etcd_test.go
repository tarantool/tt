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

func TestMakeEtcdOpts_full(t *testing.T) {
	config := cluster.NewConfig()
	expected := cluster.EtcdOpts{
		Endpoints:      []string{"foo", "bar"},
		Prefix:         "/foo",
		Username:       "user",
		Password:       "pass",
		KeyFile:        "/path/key_file",
		CertFile:       "/path/cert_file",
		CaPath:         "/path/ca_path",
		CaFile:         "/path/ca_file",
		Timeout:        3 * time.Second,
		SkipHostVerify: false,
	}
	config.Set([]string{"config", "etcd", "endpoints"}, expected.Endpoints)
	config.Set([]string{"config", "etcd", "prefix"}, expected.Prefix)
	config.Set([]string{"config", "etcd", "username"}, expected.Username)
	config.Set([]string{"config", "etcd", "password"}, expected.Password)
	config.Set([]string{"config", "etcd", "foo"}, "bar")
	config.Set([]string{"config", "etcd", "ssl", "ssl_key"}, expected.KeyFile)
	config.Set([]string{"config", "etcd", "ssl", "cert_file"}, expected.CertFile)
	config.Set([]string{"config", "etcd", "ssl", "ca_path"}, expected.CaPath)
	config.Set([]string{"config", "etcd", "ssl", "ca_file"}, expected.CaFile)
	config.Set([]string{"config", "etcd", "ssl", "foo"}, "bar")
	config.Set([]string{"config", "etcd", "http", "request", "timeout"}, 3)
	opts, err := cluster.MakeEtcdOpts(config)

	assert.NoError(t, err)
	assert.Equal(t, expected, opts)
}

func TestMakeEtcdOpts_skip_verify(t *testing.T) {
	cases := []struct {
		VerifyPeer bool
		VerifyHost bool
		Expected   bool
	}{
		{false, false, true},
		{false, true, true},
		{true, false, true},
		{true, true, false},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("%t_%t", tc.VerifyHost, tc.VerifyPeer)
		t.Run(name, func(t *testing.T) {
			config := cluster.NewConfig()
			config.Set([]string{"config", "etcd", "ssl", "verify_peer"},
				tc.VerifyPeer)
			config.Set([]string{"config", "etcd", "ssl", "verify_host"},
				tc.VerifyHost)
			opts, err := cluster.MakeEtcdOpts(config)
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, opts.SkipHostVerify)
		})
	}
}

func TestMakeEtcdOpts_empty(t *testing.T) {
	config := cluster.NewConfig()
	opts, err := cluster.MakeEtcdOpts(config)

	assert.NoError(t, err)
	assert.Equal(t, cluster.EtcdOpts{}, opts)
}

func TestMakeEtcdOpts_invalid_config(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{"config", "etcd", "ssl", "verify_host"}, "not_bool")
	_, err := cluster.MakeEtcdOpts(config)

	assert.Error(t, err)
}

func TestClientKVImplementsEtcdGetter(t *testing.T) {
	var (
		kv     clientv3.KV
		getter cluster.EtcdGetter
	)
	getter = kv
	assert.Nil(t, getter)
}

func TestNewEtcdCollector(t *testing.T) {
	var collector cluster.Collector

	collector = cluster.NewEtcdCollector(&MockEtcdGetter{}, "", 0)

	assert.NotNil(t, collector)
}

func TestEtcdConfig_Collect_getter_inputs(t *testing.T) {
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
			cluster.NewEtcdCollector(mock, tc.Prefix, 0).Collect()

			assert.NotNil(t, mock.Ctx)
			assert.Equal(t, tc.Key, mock.Key)
			require.Len(t, mock.Opts, 1)
		})
	}
}

func TestEtcdConfig_Collect_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			mock := &MockEtcdGetter{
				Err: fmt.Errorf("any"),
			}
			cluster.NewEtcdCollector(mock, "/foo", tc).Collect()

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

func TestEtcdConfig_Collect_merge(t *testing.T) {
	cases := []struct {
		Kvs      []*mvccpb.KeyValue
		Expected string
	}{
		{
			Kvs: []*mvccpb.KeyValue{
				&mvccpb.KeyValue{
					Key:   []byte("k"),
					Value: []byte("f: a\nb: a\n"),
				},
			},
			Expected: "b: a\nf: a\n",
		},
		{
			Kvs: []*mvccpb.KeyValue{
				&mvccpb.KeyValue{
					Key:   []byte("k"),
					Value: []byte("f: a\nb: a\n"),
				},
				&mvccpb.KeyValue{
					Key:   []byte("k"),
					Value: []byte("f: b\nb: b\nc: b\n"),
				},
			},
			Expected: "b: a\nc: b\nf: a\n",
		},
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			mock := &MockEtcdGetter{
				Kvs: tc.Kvs,
			}
			config, err := cluster.NewEtcdCollector(mock, "foo", 0).Collect()

			assert.NoError(t, err)
			require.NotNil(t, config)
			assert.Equal(t, tc.Expected, config.String())
		})
	}
}

func TestEtcdConfig_Collect_error(t *testing.T) {
	mock := &MockEtcdGetter{
		Err: fmt.Errorf("any"),
	}
	config, err := cluster.NewEtcdCollector(mock, "foo", 0).Collect()

	assert.ErrorContains(t, err, "failed to fetch data from etcd: any")
	assert.Nil(t, config)
}

func TestEtcdConfig_Collect_empty(t *testing.T) {
	mock := &MockEtcdGetter{
		Kvs: nil,
	}
	config, err := cluster.NewEtcdCollector(mock, "foo", 0).Collect()

	assert.Error(t, err)
	assert.Nil(t, config)
}

func TestEtcdConfig_Collect_decode_error(t *testing.T) {
	cases := [][]*mvccpb.KeyValue{
		[]*mvccpb.KeyValue{
			&mvccpb.KeyValue{Key: []byte("k"), Value: []byte("f: a\n- b\n")},
		},
		[]*mvccpb.KeyValue{
			&mvccpb.KeyValue{Key: []byte("a"), Value: []byte("f: a\n")},
			&mvccpb.KeyValue{Key: []byte("k"), Value: []byte("f: a\n- b\n")},
		},
	}

	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			mock := &MockEtcdGetter{
				Kvs: tc,
			}
			config, err := cluster.NewEtcdCollector(mock, "foo", 0).Collect()

			assert.Error(t, err)
			assert.Nil(t, config)
		})
	}
}

func TestNewEtcdDataPublisher(t *testing.T) {
	var publisher cluster.DataPublisher

	publisher = cluster.NewEtcdDataPublisher(nil, "", 0)

	assert.NotNil(t, publisher)
}

func TestEtcdDataPublisher_Publish_get_inputs(t *testing.T) {
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
			cluster.NewEtcdDataPublisher(mock, tc.Prefix, 0).Publish(data)

			assert.NotNil(t, mock.Ctx)
			assert.Equal(t, tc.Key, mock.Key)
			require.Len(t, mock.Opts, 1)
		})
	}
}

func TestEtcdDataPublisher_Publish_txn_inputs(t *testing.T) {
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
			publisher := cluster.NewEtcdDataPublisher(tc.Mock, "/foo", 0)
			publisher.Publish([]byte{})

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

func TestEtcdDataPublisher_Publish_data_nil(t *testing.T) {
	publisher := cluster.NewEtcdDataPublisher(nil, "", 0)

	err := publisher.Publish(nil)

	assert.EqualError(t, err,
		"failed to publish data into etcd: data does not exist")
}

func TestEtcdDataPublisher_Publish_publisher_nil(t *testing.T) {
	publisher := cluster.NewEtcdDataPublisher(nil, "", 0)

	assert.Panics(t, func() {
		publisher.Publish([]byte{})
	})
}

func TestEtcdDataPublisher_Publish_errors(t *testing.T) {
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
			publisher := cluster.NewEtcdDataPublisher(tc.Mock, "prefix", 0)
			err := publisher.Publish([]byte{})
			if tc.Expected != "" {
				assert.EqualError(t, err, tc.Expected)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestEtcdDataPublisher_Publish_timeout(t *testing.T) {
	cases := []time.Duration{0, 60 * time.Second}

	for _, tc := range cases {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			mock := &MockEtcdTxnGetter{}
			publisher := cluster.NewEtcdDataPublisher(mock, "prefix", tc)
			err := publisher.Publish([]byte{})

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

func TestEtcdDataPublisher_Publish_timeout_exit(t *testing.T) {
	mock := &MockEtcdTxnGetter{
		TxnRet: &MockTxn{
			Resp: &clientv3.TxnResponse{Succeeded: false},
		},
	}

	// You should increase the values if the test is flaky.
	before := time.Now()
	timeout := 100 * time.Millisecond
	delta := 10 * time.Millisecond
	publisher := cluster.NewEtcdDataPublisher(mock, "prefix", timeout)
	err := publisher.Publish([]byte{})
	assert.EqualError(t, err, "context deadline exceeded")
	assert.InDelta(t, timeout, time.Since(before), float64(delta))
}
