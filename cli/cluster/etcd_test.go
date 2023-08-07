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

func TestEtcdConfig_Collect_no_timeout(t *testing.T) {
	mock := &MockEtcdGetter{
		Err: fmt.Errorf("any"),
	}

	cluster.NewEtcdCollector(mock, "/foo", 0).Collect()
	assert.NotNil(t, mock.Ctx)
	_, ok := mock.Ctx.Deadline()
	assert.False(t, ok)
}

func TestEtcdConfig_Collect_getter_timeout(t *testing.T) {
	mock := &MockEtcdGetter{
		Err: fmt.Errorf("any"),
	}
	timeout := 2 * time.Second

	cluster.NewEtcdCollector(mock, "/foo", timeout).Collect()
	assert.NotNil(t, mock.Ctx)
	select {
	case <-time.After(timeout - time.Second):
		t.Errorf("too small timeout")
	case <-mock.Ctx.Done():
	case <-time.After(timeout + time.Second):
		t.Errorf("too long timeout")
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

	assert.NoError(t, err)
	assert.NotNil(t, config)
	_, err = config.Get(nil)
	assert.Error(t, err)
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
