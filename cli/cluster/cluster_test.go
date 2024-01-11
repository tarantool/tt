package cluster_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
)

func TestMakeClusterConfig_global(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{"foo"}, "bar")
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)

	value, err := cconfig.RawConfig.Get([]string{"foo"})
	assert.NoError(t, err)
	assert.Equal(t, "bar", value)
	assert.Len(t, cconfig.Groups, 0)
}

func TestMakeClusterConfig_group(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{"groups", "g", "car"}, "bar")
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)

	value, err := cconfig.RawConfig.Get([]string{"groups", "g", "car"})
	assert.NoError(t, err)
	assert.Equal(t, "bar", value)

	require.Contains(t, cconfig.Groups, "g")

	value, err = cconfig.Groups["g"].RawConfig.Get([]string{"car"})
	assert.NoError(t, err)
	assert.Equal(t, "bar", value)
}

func TestMakeClusterConfig_replicaset(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{"groups", "g", "replicasets", "r", "zoo"}, "bar")
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)

	value, err := cconfig.RawConfig.Get(
		[]string{"groups", "g", "replicasets", "r", "zoo"})
	assert.NoError(t, err)
	assert.Equal(t, "bar", value)

	require.Contains(t, cconfig.Groups, "g")
	require.Contains(t, cconfig.Groups["g"].Replicasets, "r")

	value, err = cconfig.Groups["g"].Replicasets["r"].RawConfig.Get([]string{"zoo"})
	assert.NoError(t, err)
	assert.Equal(t, "bar", value)
}

func TestMakeClusterConfig_instance(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{
		"groups", "g", "replicasets", "r", "instances", "i", "foo"}, "bar")
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)

	value, err := cconfig.RawConfig.Get(
		[]string{"groups", "g", "replicasets", "r", "instances", "i", "foo"})
	assert.NoError(t, err)
	assert.Equal(t, "bar", value)

	require.Contains(t, cconfig.Groups, "g")
	require.Contains(t, cconfig.Groups["g"].Replicasets, "r")
	require.Contains(t, cconfig.Groups["g"].Replicasets["r"].Instances, "i")

	value, err = cconfig.Groups["g"].Replicasets["r"].Instances["i"].
		RawConfig.Get([]string{"foo"})
	assert.NoError(t, err)
	assert.Equal(t, "bar", value)
}

func TestMakeClusterConfig_settings(t *testing.T) {
	config := cluster.NewConfig()
	expected := cluster.ClusterConfig{}
	expected.RawConfig = config
	expected.Groups = nil
	expected.Config.Etcd.Endpoints = []string{"a", "b"}
	expected.Config.Etcd.Username = "etcd_user"
	expected.Config.Etcd.Password = "etcd_pass"
	expected.Config.Etcd.Prefix = "/etcd_prefix"
	expected.Config.Etcd.Ssl.KeyFile = "etcd_keyfile"
	expected.Config.Etcd.Ssl.CertFile = "etcd_certfile"
	expected.Config.Etcd.Ssl.CaPath = "etcd_cafile"
	expected.Config.Etcd.Ssl.CaFile = "etcd_capath"
	expected.Config.Etcd.Ssl.VerifyPeer = true
	expected.Config.Etcd.Ssl.VerifyHost = true
	expected.Config.Etcd.Http.Request.Timeout = 123

	expected.Config.Storage.Prefix = "/tt_prefix"
	expected.Config.Storage.Timeout = 234
	expected.Config.Storage.Endpoints = []struct {
		Uri      string `yaml:"uri"`
		Login    string `yaml:"login"`
		Password string `yaml:"password"`
		Params   struct {
			Transport       string `yaml:"transport"`
			SslKeyFile      string `yaml:"ssl_key_file"`
			SslCertFile     string `yaml:"ssl_cert_file"`
			SslCaFile       string `yaml:"ssl_ca_file"`
			SslCiphers      string `yaml:"ssl_ciphers"`
			SslPassword     string `yaml:"ssl_password"`
			SslPasswordFile string `yaml:"ssl_password_file"`
		} `yaml:"params"`
	}{
		{
			Uri:      "tt_uri",
			Login:    "tt_login",
			Password: "tt_password",
		},
	}
	expected.Config.Storage.Endpoints[0].Params.Transport = "tt_transport"
	expected.Config.Storage.Endpoints[0].Params.SslKeyFile = "tt_key_file"
	expected.Config.Storage.Endpoints[0].Params.SslCertFile = "tt_cert_file"
	expected.Config.Storage.Endpoints[0].Params.SslCaFile = "tt_ca_file"
	expected.Config.Storage.Endpoints[0].Params.SslCiphers = "tt_ciphers"
	expected.Config.Storage.Endpoints[0].Params.SslPassword = "tt_password"
	expected.Config.Storage.Endpoints[0].Params.SslPasswordFile = "tt_password_file"

	config.Set([]string{"config", "etcd", "endpoints"},
		[]any{expected.Config.Etcd.Endpoints[0], expected.Config.Etcd.Endpoints[1]})
	config.Set([]string{"config", "etcd", "username"},
		expected.Config.Etcd.Username)
	config.Set([]string{"config", "etcd", "password"},
		expected.Config.Etcd.Password)
	config.Set([]string{"config", "etcd", "prefix"},
		expected.Config.Etcd.Prefix)
	config.Set([]string{"config", "etcd", "ssl", "ssl_key"},
		expected.Config.Etcd.Ssl.KeyFile)
	config.Set([]string{"config", "etcd", "ssl", "cert_file"},
		expected.Config.Etcd.Ssl.CertFile)
	config.Set([]string{"config", "etcd", "ssl", "ca_path"},
		expected.Config.Etcd.Ssl.CaPath)
	config.Set([]string{"config", "etcd", "ssl", "ca_file"},
		expected.Config.Etcd.Ssl.CaFile)
	config.Set([]string{"config", "etcd", "ssl", "verify_host"},
		expected.Config.Etcd.Ssl.VerifyHost)
	config.Set([]string{"config", "etcd", "ssl", "verify_peer"},
		expected.Config.Etcd.Ssl.VerifyPeer)
	config.Set([]string{"config", "etcd", "http", "request", "timeout"},
		int(expected.Config.Etcd.Http.Request.Timeout))

	config.Set([]string{"config", "storage", "prefix"},
		expected.Config.Storage.Prefix)
	config.Set([]string{"config", "storage", "timeout"},
		int(expected.Config.Storage.Timeout))

	config.Set([]string{"config", "storage", "endpoints"},
		[]any{
			map[any]any{
				"uri":      "tt_uri",
				"login":    "tt_login",
				"password": "tt_password",
				"params": map[any]any{
					"transport":         "tt_transport",
					"ssl_key_file":      "tt_key_file",
					"ssl_cert_file":     "tt_cert_file",
					"ssl_ca_file":       "tt_ca_file",
					"ssl_ciphers":       "tt_ciphers",
					"ssl_password":      "tt_password",
					"ssl_password_file": "tt_password_file",
				},
			},
		},
	)

	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	assert.Equal(t, expected.Config, cconfig.Config)
	assert.Equal(t, expected.Groups, cconfig.Groups)

	expected.RawConfig.ForEach(nil, func(path []string, value any) {
		v, err := cconfig.RawConfig.Get(path)
		assert.NoError(t, err)
		assert.Equal(t, value, v)
	})
}

func TestMakeClusterConfig_empty(t *testing.T) {
	config := cluster.NewConfig()
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)

	_, err = cconfig.RawConfig.Get(nil)
	assert.Error(t, err)
	assert.Len(t, cconfig.Groups, 0)
}

func TestMakeClusterConfig_error(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{"groups"}, true)
	_, err := cluster.MakeClusterConfig(config)
	require.Error(t, err)
}

func TestInstances(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{"foo"}, "bar")
	config.Set([]string{"groups", "g", "foo"}, "bar")
	config.Set([]string{"groups", "g", "replicasets", "rr", "foo"}, "bar")
	config.Set([]string{
		"groups", "g", "replicasets", "r", "instances", "a", "foo"}, "bar")
	config.Set([]string{
		"groups", "g", "replicasets", "rr", "instances", "b", "foo"}, "bar")
	config.Set([]string{
		"groups", "g", "foo"}, "bar")
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)

	assert.ElementsMatch(t, cluster.Instances(cconfig), []string{"a", "b"})
}

func TestInstances_empty(t *testing.T) {
	config := cluster.NewConfig()
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)

	assert.Len(t, cluster.Instances(cconfig), 0)
}

func TestHasInstance(t *testing.T) {
	name := "foo"
	cases := []struct {
		path     []string
		expected bool
	}{
		{[]string{name}, false},
		{[]string{"app", name}, false},
		{[]string{"groups", name, "bar"}, false},
		{[]string{"groups", name, name}, false},
		{[]string{"groups", name, "replicasets", name, "bar"}, false},
		{[]string{"groups", name, "replicasets", name, "instances", name, "bar"}, true},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.path, "_"), func(t *testing.T) {
			config := cluster.NewConfig()
			config.Set(tc.path, true)

			cconfig, err := cluster.MakeClusterConfig(config)
			require.NoError(t, err)
			require.NotNil(t, cconfig.RawConfig)
			assert.Equal(t, tc.expected, cluster.HasInstance(cconfig, name))
		})
	}
}

type expectedInstantiate struct {
	path  []string
	value any
}

func assertExpectedInstantiate(t *testing.T,
	config *cluster.Config, expected []expectedInstantiate) {
	t.Helper()

	expectedPaths := [][]string{}
	for _, e := range expected {
		value, err := config.Get(e.path)
		assert.NoError(t, err)
		assert.Equal(t, e.value, value)
		expectedPaths = append(expectedPaths, e.path)
	}

	foreachPaths := [][]string{}
	config.ForEach(nil, func(path []string, value any) {
		foreachPaths = append(foreachPaths, path)
	})

	assert.ElementsMatch(t, expectedPaths, foreachPaths)
}

func TestInstantiate_global(t *testing.T) {
	config := cluster.NewConfig()
	config.Set([]string{"foo"}, 1)
	config.Set([]string{"bar", "foo"}, 2)
	config.Set([]string{"groups", "a", "replicasets", "b", "instances", "c", "x"}, 3)
	config.Set([]string{"groups", "a", "replicasets", "b", "x"}, 4)
	config.Set([]string{"groups", "a", "x"}, 5)

	expected := []expectedInstantiate{
		{[]string{"foo"}, 1},
		{[]string{"bar", "foo"}, 2},
	}

	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)
	iconfig := cluster.Instantiate(cconfig, "non_exist")
	assertExpectedInstantiate(t, iconfig, expected)
}

func TestInstantiate_inherbit(t *testing.T) {
	config := cluster.NewConfig()
	// Priority.
	config.Set([]string{"groups", "a", "replicasets", "b", "instances", "exist", "foo"}, 1)
	config.Set([]string{"groups", "a", "replicasets", "b", "foo"}, 2)
	config.Set([]string{"groups", "a", "foo"}, 3)
	config.Set([]string{"foo"}, 4)

	config.Set([]string{"groups", "a", "replicasets", "b", "car"}, 2)
	config.Set([]string{"groups", "a", "car"}, 3)
	config.Set([]string{"car"}, 4)

	config.Set([]string{"groups", "a", "voo"}, 3)
	config.Set([]string{"voo"}, 4)

	config.Set([]string{"boo"}, 4)
	config.Set([]string{"bar", "foo"}, 4)

	// Other instances/replicasets/groups.
	config.Set([]string{"groups", "a", "replicasets", "b", "instances", "x", "tar"}, 3)
	config.Set([]string{"groups", "a", "replicasets", "x", "instances", "b", "tar"}, 3)
	config.Set([]string{"groups", "x", "replicasets", "b", "instances", "b", "tar"}, 3)
	config.Set([]string{"groups", "a", "replicasets", "x", "tar"}, 4)
	config.Set([]string{"groups", "x", "tar"}, 4)
	config.Set([]string{"groups", "x", "foo"}, 5)

	expected := []expectedInstantiate{
		{[]string{"foo"}, 1},
		{[]string{"car"}, 2},
		{[]string{"voo"}, 3},
		{[]string{"boo"}, 4},
		{[]string{"bar", "foo"}, 4},
	}

	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)
	require.NotNil(t, cconfig.RawConfig)
	iconfig := cluster.Instantiate(cconfig, "exist")
	assertExpectedInstantiate(t, iconfig, expected)
}

func TestGetClusterConfig_path(t *testing.T) {
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
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
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
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
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "some/non/exist")

	assert.Error(t, err)
	assert.NotNil(t, config)
}

func TestGetClusterConfig_nopath(t *testing.T) {
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
	config, err := cluster.GetClusterConfig(collectors, "")
	expected := "a configuration file must be set"

	assert.EqualError(t, err, expected)
	assert.NotNil(t, config)
}

func TestGetInstanceConfig_file(t *testing.T) {
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
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
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
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
	collectors := cluster.NewCollectorFactory(cluster.NewDataCollectorFactory())
	cconfig, err := cluster.GetClusterConfig(collectors, "testdata/app/config.yaml")
	require.NoError(t, err)
	_, err = cluster.GetInstanceConfig(cconfig, "unknown")
	expected := "an instance \"unknown\" not found"

	assert.EqualError(t, err, expected)
}

func TestReplaceInstanceConfig_not_found(t *testing.T) {
	config := cluster.NewConfig()
	cconfig, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)

	cconfig, err = cluster.ReplaceInstanceConfig(cconfig, "any", cluster.NewConfig())
	assert.EqualError(t, err, "cluster configuration has not an instance \"any\"")
}

func TestReplaceInstanceConfig_replcase(t *testing.T) {
	path := []string{"groups", "a", "replicasets", "b", "instances", "c", "foo"}
	config := cluster.NewConfig()
	err := config.Set(path, 1)
	require.NoError(t, err)
	old, err := cluster.MakeClusterConfig(config)
	require.NoError(t, err)

	replace := cluster.NewConfig()
	err = replace.Set([]string{"foo"}, 2)
	require.NoError(t, err)

	newConfig, err := cluster.ReplaceInstanceConfig(old, "c", replace)
	require.NoError(t, err)
	oldValue, err := old.RawConfig.Get(path)
	require.NoError(t, err)
	newValue, err := newConfig.RawConfig.Get(path)
	require.NoError(t, err)

	assert.Equal(t, 1, oldValue)
	assert.Equal(t, 2, newValue)
}
