package cluster_test

import (
	"context"
	"fmt"
	"net/url"
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

func TestMakeEtcdOptsFromUrl(t *testing.T) {
	const defaultTimeout = 3 * time.Second

	cases := []struct {
		Url  string
		Opts cluster.EtcdOpts
		Err  string
	}{
		{
			Url:  "",
			Opts: cluster.EtcdOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url:  "host",
			Opts: cluster.EtcdOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url:  "scheme:///prefix",
			Opts: cluster.EtcdOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url: "scheme://localhost",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost:3013",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost:3013"},
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://user@localhost",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				Username:  "user",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://user:pass@localhost",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				Username:  "user",
				Password:  "pass",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				Prefix:    "/",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/prefix",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				Prefix:    "/prefix",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_key_file=/any/kfile",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				KeyFile:   "/any/kfile",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_cert_file=/any/certfile",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				CertFile:  "/any/certfile",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_ca_path=/any/capath",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				CaPath:    "/any/capath",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_ca_file=/any/cafile",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				CaFile:    "/any/cafile",
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?verify_peer=true&verify_host=true",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				Timeout:   defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?verify_peer=false",
			Opts: cluster.EtcdOpts{
				Endpoints:      []string{"scheme://localhost"},
				SkipHostVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?verify_peer=asd",
			Opts: cluster.EtcdOpts{},
			Err:  "invalid verify_peer, boolean expected",
		},
		{
			Url: "scheme://localhost?verify_host=false",
			Opts: cluster.EtcdOpts{
				Endpoints:      []string{"scheme://localhost"},
				SkipHostVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?verify_host=asd",
			Opts: cluster.EtcdOpts{},
			Err:  "invalid verify_host, boolean expected",
		},
		{
			Url: "scheme://localhost?timeout=5.5",
			Opts: cluster.EtcdOpts{
				Endpoints: []string{"scheme://localhost"},
				Timeout:   time.Duration(float64(5.5) * float64(time.Second)),
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?timeout=asd",
			Opts: cluster.EtcdOpts{},
			Err:  "invalid timeout, float expected",
		},
		{
			Url: "scheme://user:pass@localhost:2012/prefix" +
				"?ssl_key_file=kfile&ssl_cert_file=certfile" +
				"&ssl_ca_path=capath&ssl_ca_file=cafile" +
				"&verify_peer=true&verify_host=false&timeout=2",
			Opts: cluster.EtcdOpts{
				Endpoints:      []string{"scheme://localhost:2012"},
				Prefix:         "/prefix",
				Username:       "user",
				Password:       "pass",
				KeyFile:        "kfile",
				CertFile:       "certfile",
				CaPath:         "capath",
				CaFile:         "cafile",
				SkipHostVerify: true,
				Timeout:        time.Duration(2 * time.Second),
			},
			Err: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Url, func(t *testing.T) {
			uri, err := url.Parse(tc.Url)
			require.NoError(t, err)

			opts, err := cluster.MakeEtcdOptsFromUrl(uri)
			if tc.Err != "" {
				assert.ErrorContains(t, err, tc.Err)
			} else {
				assert.Equal(t, tc.Opts, opts)
			}
		})
	}
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
