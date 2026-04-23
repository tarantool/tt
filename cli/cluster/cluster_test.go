package cluster_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
	libcluster "github.com/tarantool/tt/lib/cluster"
)

// spell-checker:ignore nopath noinstance

// clearAmbientTTEnv removes all TT_* environment variables that were already
// set before the test runs, so that the builder sees only those set
// explicitly by the test itself. Removed variables are restored via t.Cleanup.
func clearAmbientTTEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TT_") {
			continue
		}
		k := strings.SplitN(kv, "=", 2)[0]
		saved := os.Getenv(k)
		os.Unsetenv(k)
		t.Cleanup(func() { os.Setenv(k, saved) })
	}
}

func TestGetClusterConfig_path(t *testing.T) {
	clearAmbientTTEnv(t)
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
	clearAmbientTTEnv(t)
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
	clearAmbientTTEnv(t)
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
	clearAmbientTTEnv(t)
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

func TestGetClusterConfig_env_two_tier_priority(t *testing.T) {
	collectors := libcluster.NewCollectorFactory(libcluster.NewDataCollectorFactory())

	cases := []struct {
		name           string
		mainEnv        string // TT_REPLICATION_FAILOVER value ("" means unset)
		defaultEnv     string // TT_REPLICATION_FAILOVER_DEFAULT value ("" means unset)
		expectedValue  string
	}{
		{
			name:          "main env only",
			mainEnv:       "manual",
			defaultEnv:    "",
			expectedValue: "manual",
		},
		{
			name:          "default env only",
			mainEnv:       "",
			defaultEnv:    "election",
			expectedValue: "election",
		},
		{
			name:          "main wins over default",
			mainEnv:       "manual",
			defaultEnv:    "election",
			expectedValue: "manual",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mainEnv != "" {
				t.Setenv("TT_REPLICATION_FAILOVER", tc.mainEnv)
			}
			if tc.defaultEnv != "" {
				t.Setenv("TT_REPLICATION_FAILOVER_DEFAULT", tc.defaultEnv)
			}

			cconfig, err := cluster.GetClusterConfig(collectors, "testdata/app/config.yaml")
			require.NoError(t, err)

			got, err := cconfig.RawConfig.Get([]string{"replication", "failover"})
			require.NoError(t, err)
			assert.Equal(t, tc.expectedValue, got)
		})
	}
}
