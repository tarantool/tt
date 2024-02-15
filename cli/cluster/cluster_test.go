package cluster_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

func TestGetClusterConfig_path(t *testing.T) {
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "testdata/app/config.yaml")

	require.NoError(t, err)
	assert.Equal(t, `app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
groups:
  a:
    bar: 2
    foo: 2
    replicasets:
      b:
        bar: 3
        foo: 3
        instances:
          c:
            foo: 4
    zoo: 2
  b:
    replicasets:
      b:
        instances:
          b:
            too: 3
        too: 3
    too: 2
wal:
  dir: filedir
`, config.RawConfig.String())
	require.Contains(t, config.Groups, "a")
	assert.Equal(t, `bar: 2
foo: 2
replicasets:
  b:
    bar: 3
    foo: 3
    instances:
      c:
        foo: 4
zoo: 2
`, config.Groups["a"].RawConfig.String())
	require.Contains(t, config.Groups["a"].Replicasets, "b")
	assert.Equal(t, `bar: 3
foo: 3
instances:
  c:
    foo: 4
`, config.Groups["a"].Replicasets["b"].RawConfig.String())
	require.Contains(t, config.Groups["a"].Replicasets["b"].Instances, "c")
	assert.Equal(t, `foo: 4
`, config.Groups["a"].Replicasets["b"].Instances["c"].RawConfig.String())
	require.Contains(t, config.Groups, "b")
	assert.Equal(t, `replicasets:
  b:
    instances:
      b:
        too: 3
    too: 3
too: 2
`, config.Groups["b"].RawConfig.String())
	require.Contains(t, config.Groups["b"].Replicasets, "b")
	assert.Equal(t, `instances:
  b:
    too: 3
too: 3
`, config.Groups["b"].Replicasets["b"].RawConfig.String())
	require.Contains(t, config.Groups["b"].Replicasets["b"].Instances, "b")
	assert.Equal(t, `too: 3
`, config.Groups["b"].Replicasets["b"].Instances["b"].RawConfig.String())
}

func TestGetClusterConfig_environment(t *testing.T) {
	os.Setenv("TT_WAL_DIR_DEFAULT", "envdir")
	os.Setenv("TT_WAL_MODE_DEFAULT", "envmode")
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "testdata/app/config.yaml")

	os.Unsetenv("TT_WAL_DIR_DEFAULT")
	os.Unsetenv("TT_WAL_MODE_DEFAULT")

	require.NoError(t, err)
	assert.Equal(t, `app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
groups:
  a:
    bar: 2
    foo: 2
    replicasets:
      b:
        bar: 3
        foo: 3
        instances:
          c:
            foo: 4
    zoo: 2
  b:
    replicasets:
      b:
        instances:
          b:
            too: 3
        too: 3
    too: 2
wal:
  dir: filedir
  mode: envmode
`, config.RawConfig.String())
}

func TestGetClusterConfig_invalid_apppath(t *testing.T) {
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "some/non/exist")

	assert.Error(t, err)
	assert.NotNil(t, config)
}

func TestGetClusterConfig_nopath(t *testing.T) {
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "")
	expected := "a configuration file must be set"

	assert.EqualError(t, err, expected)
	assert.NotNil(t, config)
}

func TestGetInstanceConfig_file(t *testing.T) {
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	cconfig, err := cluster.GetClusterConfig(collectors, "testdata/app/config.yaml")
	require.NoError(t, err)
	config, err := cluster.GetInstanceConfig(cconfig, "c")

	require.NoError(t, err)
	assert.Equal(t, `app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
bar: 3
foo: 4
wal:
  dir: filedir
zoo: 2
`, config.RawConfig.String())
}

func TestGetInstanceConfig_environment(t *testing.T) {
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	cconfig, err := cluster.GetClusterConfig(collectors, "testdata/app/config.yaml")
	require.NoError(t, err)
	os.Setenv("TT_WAL_DIR", "envdir")
	config, err := cluster.GetInstanceConfig(cconfig, "c")
	os.Unsetenv("TT_WAL_DIR")

	require.NoError(t, err)
	require.Equal(t, `app:
  bar: 1
  foo: 1
  hoo: 1
  zoo: 1
bar: 3
foo: 4
wal:
  dir: envdir
zoo: 2
`, config.RawConfig.String())
}

func TestGetInstanceConfig_noinstance(t *testing.T) {
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())
	cconfig, err := cluster.GetClusterConfig(collectors, "testdata/app/config.yaml")
	require.NoError(t, err)
	_, err = cluster.GetInstanceConfig(cconfig, "unknown")
	expected := "an instance \"unknown\" not found"

	assert.EqualError(t, err, expected)
}
