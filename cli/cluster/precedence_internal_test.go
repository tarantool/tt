package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/integrity"
)

// precedenceFile is the config file of the precedence tests: what Tarantool
// 3.9 was run with, a config storage source put where etcd goes.
const precedenceFile = `labels:
  b: file-global
groups:
  g:
    replicasets:
      r:
        instances:
          i1:
            labels:
              a: file-instance
            snapshot:
              count: 9
`

// precedenceStorage is the config in the storage of the precedence tests.
const precedenceStorage = `labels:
  a: storage-global
  k: storage-global
replication:
  failover: manual
groups:
  g:
    replicasets:
      r:
        instances:
          i1:
            labels:
              b: storage-instance
              d: storage-instance
            memtx:
              memory: 300000000
`

// clearTTEnv unsets the TT_* variables of the environment for the test.
func clearTTEnv(t *testing.T) {
	t.Helper()

	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "TT_") {
			t.Setenv(name, "")
			require.NoError(t, os.Unsetenv(name))
		}
	}
}

// instanceOf builds the cluster config of file and storage and returns the
// effective config of the instance i1 as Go values.
func instanceOf(t *testing.T, file, storage string) map[string]any {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(file), 0o600))

	cfg, err := buildClusterConfig(t.Context(), path, []byte(storage))
	require.NoError(t, err)

	inst, err := InstanceConfig(cfg.Snapshot(), "i1")
	require.NoError(t, err)

	var got map[string]any

	_, err = inst.Get(nil, &got)
	require.NoError(t, err)

	return got
}

// TestBuildClusterConfigSourcesOverScopes checks the config of an instance
// against what Tarantool 3.9 gives: the config of the instance is made of
// each source on its own, its scopes merged, and the sources merge by their
// priority, TT_* > file > storage > TT_*_DEFAULT. A global key of the file
// wins over an instance key of the storage, an instance key of the file over
// a global one of the storage, and TT_* over an instance key of the storage.
func TestBuildClusterConfigSourcesOverScopes(t *testing.T) {
	clearTTEnv(t)
	t.Setenv("TT_MEMTX_MEMORY", "268435456")
	t.Setenv("TT_SNAPSHOT_COUNT_DEFAULT", "3")
	t.Setenv("TT_REPLICATION_FAILOVER_DEFAULT", "off")

	got := instanceOf(t, precedenceFile, precedenceStorage)

	assert.Equal(t, map[string]any{
		"a": "file-instance",
		"b": "file-global",
		"k": "storage-global",
		"d": "storage-instance",
	}, got["labels"])
	assert.Equal(t, map[string]any{"memory": int64(268435456)}, got["memtx"])
	assert.Equal(t, map[string]any{"count": int64(9)}, got["snapshot"],
		"an instance key of the file over a default of the environment")
	assert.Equal(t, map[string]any{"failover": "manual"}, got["replication"],
		"the storage over a default of the environment")
}

// TestBuildClusterConfigWithoutStorage checks that TT_*_DEFAULT is the lowest
// source for a file without a config storage too: a default fills a key the
// file has at no scope of the instance, and loses to one it has at any.
func TestBuildClusterConfigWithoutStorage(t *testing.T) {
	clearTTEnv(t)
	t.Setenv("TT_SNAPSHOT_COUNT_DEFAULT", "3")
	t.Setenv("TT_LABELS_DEFAULT", "b=default")
	t.Setenv("TT_REPLICATION_FAILOVER_DEFAULT", "manual")

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(precedenceFile), 0o600))

	cfg, err := GetClusterConfig(t.Context(), path, integrity.IntegrityCtx{})
	require.NoError(t, err)

	inst, err := InstanceConfig(cfg.Snapshot(), "i1")
	require.NoError(t, err)

	var got map[string]any

	_, err = inst.Get(nil, &got)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"count": int64(9)}, got["snapshot"])
	assert.Equal(t, map[string]any{"a": "file-instance", "b": "file-global"}, got["labels"])
	assert.Equal(t, map[string]any{"failover": "manual"}, got["replication"])
}

// TestBuildClusterConfigWhole checks the cluster config as a whole: the
// file over the storage key by key, at the scope each sits in.
func TestBuildClusterConfigWhole(t *testing.T) {
	clearTTEnv(t)

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(precedenceFile), 0o600))

	cfg, err := buildClusterConfig(t.Context(), path, []byte(precedenceStorage))
	require.NoError(t, err)

	var labels map[string]any

	_, err = cfg.Get(goconfig.NewKeyPath("labels"), &labels)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"a": "storage-global", "b": "file-global", "k": "storage-global",
	}, labels)

	_, err = cfg.Get(goconfig.NewKeyPath("groups/g/replicasets/r/instances/i1/labels"), &labels)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{
		"a": "file-instance", "b": "storage-instance", "d": "storage-instance",
	}, labels)
}
