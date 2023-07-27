package cluster_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

var testFormatter = func(path []string) string {
	middle := strings.ToUpper(strings.Join(path, "_"))
	return fmt.Sprintf("TT_%s_DEFAULT", middle)
}

func TestNewEnvCollector(t *testing.T) {
	var collector cluster.Collector
	collector = cluster.NewEnvCollector(testFormatter)
	assert.NotNil(t, collector)
}

func TestEnvCollector_Collect_all(t *testing.T) {
	for i, p := range cluster.ConfigEnvPaths {
		env := testFormatter(p)
		os.Setenv(env, fmt.Sprintf("%d", i))
	}

	collector := cluster.NewEnvCollector(testFormatter)
	config, err := collector.Collect()
	require.NoError(t, err)

	for i, p := range cluster.ConfigEnvPaths {
		value, err := config.Get(p)
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("%d", i), value)
	}
}

func TestEnvCollector_Collect_nothing(t *testing.T) {
	for _, p := range cluster.ConfigEnvPaths {
		env := testFormatter(p)
		os.Unsetenv(env)
	}

	collector := cluster.NewEnvCollector(testFormatter)
	config, err := collector.Collect()
	require.NoError(t, err)

	for _, p := range cluster.ConfigEnvPaths {
		_, err := config.Get(p)
		assert.Error(t, err)
	}
}

func TestNewYamlCollector_unique(t *testing.T) {
	collector := cluster.NewEnvCollector(testFormatter)
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
