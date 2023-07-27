package cluster_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

func TestNewYamlCollector(t *testing.T) {
	var collector cluster.Collector
	collector = cluster.NewYamlCollector(nil)
	assert.NotNil(t, collector)
}

func TestYamlCollector_valid(t *testing.T) {
	data := []byte(`config:
  version: 3.0.0
  hooks:
    post_cfg: /foo
    on_state_change: /bar
etcd:
  endpoints:
    - http://foo:4001
    - bar
  username: etcd
  password: not_a_secret`)
	paths := []struct {
		path  []string
		value any
	}{
		{[]string{"config", "version"}, "3.0.0"},
		{[]string{"config", "hooks", "post_cfg"}, "/foo"},
		{[]string{"config", "hooks", "on_state_change"}, "/bar"},
		{[]string{"etcd", "endpoints"}, []any{"http://foo:4001", "bar"}},
		{[]string{"etcd", "username"}, "etcd"},
		{[]string{"etcd", "password"}, "not_a_secret"},
	}
	collector := cluster.NewYamlCollector(data)

	config, err := collector.Collect()
	require.NoError(t, err)
	require.NotNil(t, config)

	for _, p := range paths {
		t.Run(fmt.Sprintf("%v", p.path), func(t *testing.T) {
			value, err := config.Get(p.path)
			assert.NoError(t, err)
			assert.Equal(t, p.value, value)
		})
	}
}

func TestYamlCollector_invalid(t *testing.T) {
	data := []byte(`config: asd
- foo
`)
	collector := cluster.NewYamlCollector(data)

	_, err := collector.Collect()
	assert.Error(t, err)
}

func TestYamlCollector_empty(t *testing.T) {
	data := [][]byte{
		nil,
		[]byte(""),
	}

	for _, d := range data {
		t.Run(fmt.Sprintf("%v", data), func(t *testing.T) {
			collector := cluster.NewYamlCollector(d)
			config, err := collector.Collect()

			assert.NoError(t, err)
			assert.NotNil(t, config)
		})
	}
}

func TestYamlCollector_unique(t *testing.T) {
	collector := cluster.NewYamlCollector([]byte("config: asd"))
	require.NotNil(t, collector)

	config1, err := collector.Collect()
	require.NoError(t, err)
	config2, err := collector.Collect()
	require.NoError(t, err)

	path := []string{"foo"}
	require.Nil(t, config1.Set(path, "bar"))
	_, err = config2.Get(path)
	assert.Error(t, err)
}
