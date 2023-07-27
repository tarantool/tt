package cluster_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

const (
	invalidYamlPath = "testdata/invalid.yaml"
	testYamlPath    = "testdata/test.yaml"
)

func TestNewFileCollector(t *testing.T) {
	var collector cluster.Collector

	collector = cluster.NewFileCollector(testYamlPath)

	assert.NotNil(t, collector)
}

func TestNewFileCollector_not_exist(t *testing.T) {
	collector := cluster.NewFileCollector("some/invalid/path")

	_, err := collector.Collect()
	assert.Error(t, err)
}

func TestFileCollector_valid(t *testing.T) {
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
	collector := cluster.NewFileCollector(testYamlPath)

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

func TestFileCollector_invalid(t *testing.T) {
	collector := cluster.NewFileCollector(invalidYamlPath)

	_, err := collector.Collect()
	assert.Error(t, err)
}
