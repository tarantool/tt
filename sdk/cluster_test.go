package sdk_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/internal/clusteryaml"
)

// clusterYAML is a cluster configuration setting a value at every level of
// the hierarchy, and credentials at two of them.
const clusterYAML = `
credentials:
  users:
    admin:
      password: secret
iproto:
  listen:
    - uri: 127.0.0.1:3300
log:
  level: 5
groups:
  g:
    log:
      level: 6
    replicasets:
      r:
        database:
          mode: ro
        instances:
          b:
            iproto:
              listen:
                - uri: 127.0.0.1:3302
          a:
            credentials:
              users:
                guest:
                  roles: [super]
            database:
              mode: rw
  h:
    replicasets:
      s:
        instances:
          c: {}
`

// buildCluster builds data the way the SDK does for the core's tests.
func buildCluster(t *testing.T, data string) goconfig.Config {
	t.Helper()

	cfg, err := clusteryaml.Build(context.Background(), []byte(data))
	require.NoError(t, err)

	return cfg
}

// get returns the value at path in cfg.
func get(t *testing.T, cfg goconfig.Config, path string) any {
	t.Helper()

	var value any

	_, err := cfg.Get(goconfig.NewKeyPath(path), &value)
	require.NoError(t, err, path)

	return value
}

// TestSplitInstance checks the application and instance of references with
// and without the delimiter.
func TestSplitInstance(t *testing.T) {
	t.Parallel()

	for ref, want := range map[string][2]string{
		"app":          {"app", ""},
		"app:inst":     {"app", "inst"},
		"app:":         {"app", ""},
		":inst":        {"", "inst"},
		"":             {"", ""},
		"app:inst:tag": {"app", "inst:tag"},
	} {
		app, instance := sdk.SplitInstance(ref)
		assert.Equal(t, want, [2]string{app, instance}, ref)
	}
}

// TestParseClusterSource checks that a storage URI, an existing regular file
// and anything else are told apart, and that credentials go to a storage
// source only.
func TestParseClusterSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(file, []byte("{}"), 0o600))

	creds := sdk.Credentials{Username: "user", Password: "pass"}

	storage := func(arg string) sdk.ClusterSource { return sdk.StorageSource(arg, creds) }

	for arg, want := range map[string]func(string) sdk.ClusterSource{
		"http://localhost:2379/prefix":            storage,
		"https://u:p@localhost:2379/tt?timeout=2": storage,
		"tcp://localhost:3301":                    storage,
		file:                                      sdk.FileSource,
		dir:                                       sdk.AppSource,
		filepath.Join(dir, "missing.yaml"):        sdk.AppSource,
		"app":                                     sdk.AppSource,
		"app:inst":                                sdk.AppSource,
		"http://localhost:2379/prefix?timeout=never": sdk.AppSource,
	} {
		assert.Equal(t, want(arg), sdk.ParseClusterSource(arg, creds), arg)
	}
}

// TestClusterSourceAccessors checks that each constructor is told by its
// own accessor only, and that sources compare by what they name.
func TestClusterSourceAccessors(t *testing.T) {
	t.Parallel()

	creds := sdk.Credentials{Username: "user", Password: "pass"}

	name, ok := sdk.AppSource("app").App()
	assert.True(t, ok)
	assert.Equal(t, "app", name)

	_, ok = sdk.AppSource("app").File()
	assert.False(t, ok)

	path, ok := sdk.FileSource("/c.yaml").File()
	assert.True(t, ok)
	assert.Equal(t, "/c.yaml", path)

	_, _, ok = sdk.FileSource("/c.yaml").Storage()
	assert.False(t, ok)

	uri, got, ok := sdk.StorageSource("http://h:1/p", creds).Storage()
	assert.True(t, ok)
	assert.Equal(t, "http://h:1/p", uri)
	assert.Equal(t, creds, got)

	_, ok = sdk.StorageSource("http://h:1/p", creds).App()
	assert.False(t, ok)

	var zero sdk.ClusterSource

	_, ok = zero.App()
	assert.False(t, ok)

	_, ok = zero.File()
	assert.False(t, ok)

	_, _, ok = zero.Storage()
	assert.False(t, ok)

	sources := map[sdk.ClusterSource]int{sdk.AppSource("x"): 1, sdk.FileSource("x"): 2}
	assert.Equal(t, 1, sources[sdk.AppSource("x")])
	assert.Equal(t, 2, sources[sdk.FileSource("x")])
	assert.NotEqual(t, sdk.StorageSource("http://h:1/p", creds),
		sdk.StorageSource("http://h:1/p", sdk.Credentials{}))
}

// TestClusterSourceString checks that a source's description never shows
// credentials.
func TestClusterSourceString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `application "app"`, sdk.AppSource("app").String())
	assert.Equal(t, `file "/c.yaml"`, sdk.FileSource("/c.yaml").String())

	storage := sdk.StorageSource("http://user:hunter2@localhost:2379/prefix",
		sdk.Credentials{Username: "other", Password: "swordfish"}).String()
	assert.Equal(t, `storage "http://localhost:2379/prefix"`, storage)
	assert.NotContains(t, storage, "hunter2")
	assert.NotContains(t, storage, "swordfish")
}

// TestInstances checks that the instances of every group and replicaset are
// listed, sorted.
func TestInstances(t *testing.T) {
	t.Parallel()

	names, err := sdk.Instances(buildCluster(t, clusterYAML))
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, names)

	names, err = sdk.Instances(buildCluster(t, ""))
	require.NoError(t, err)
	assert.Empty(t, names)
}

// TestInstanceConfigInheritance checks that an instance sees what it sets
// over its replicaset's, its group's and the global values, with credentials
// merged across levels.
func TestInstanceConfigInheritance(t *testing.T) {
	t.Parallel()

	cfg := buildCluster(t, clusterYAML)

	instA, err := sdk.InstanceConfig(cfg, "a")
	require.NoError(t, err)
	assert.Equal(t, "rw", get(t, instA, "database/mode"), "the instance's own value")
	assert.EqualValues(t, 6, get(t, instA, "log/level"), "the group's value")
	assert.Equal(t, []any{map[string]any{"uri": "127.0.0.1:3300"}},
		get(t, instA, "iproto/listen"), "the global value")
	assert.Equal(t, "secret", get(t, instA, "credentials/users/admin/password"),
		"global credentials merged in")
	assert.Equal(t, []any{"super"}, get(t, instA, "credentials/users/guest/roles"),
		"the instance's credentials merged in")

	instB, err := sdk.InstanceConfig(cfg, "b")
	require.NoError(t, err)
	assert.Equal(t, "ro", get(t, instB, "database/mode"), "the replicaset's value")
	assert.Equal(t, []any{map[string]any{"uri": "127.0.0.1:3302"}},
		get(t, instB, "iproto/listen"), "the instance's value over the global one")

	instC, err := sdk.InstanceConfig(cfg, "c")
	require.NoError(t, err)
	assert.EqualValues(t, 5, get(t, instC, "log/level"), "another group sees the global value")
}

// TestInstanceConfigNotFound checks that an instance the configuration does
// not declare is sdk.ErrNotFound, named in the message.
func TestInstanceConfigNotFound(t *testing.T) {
	t.Parallel()

	_, err := sdk.InstanceConfig(buildCluster(t, clusterYAML), "r")
	require.ErrorIs(t, err, sdk.ErrNotFound)
	assert.EqualError(t, err, `instance "r" not found`)
}

// TestClusterHelpersNeedHierarchy checks that a configuration built without
// the Tarantool hierarchy is an error rather than an empty answer.
func TestClusterHelpersNeedHierarchy(t *testing.T) {
	t.Parallel()

	builder := goconfig.NewBuilder()

	cfg, errs := builder.Build(context.Background())
	require.Empty(t, errs)

	_, err := sdk.Instances(cfg)
	require.ErrorIs(t, err, goconfig.ErrNoInheritance)

	_, err = sdk.InstanceConfig(cfg, "a")
	require.ErrorIs(t, err, goconfig.ErrNoInheritance)
	assert.NotErrorIs(t, err, sdk.ErrNotFound)
}
