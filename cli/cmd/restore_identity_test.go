package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/restore"
)

// noEnv is an environment that sets nothing.
func noEnv(string) string { return "" }

// The identity is read off the instantiated configuration like the
// directories are: a UUID or a setting stated for the replicaset reaches the
// instance that inherits it, and the replicaset name is where the instance is
// declared.
func TestInstanceConfigIdentity_ReadsInheritedKeys(t *testing.T) {
	path := writeClusterConfig(t, `groups:
  storages:
    replicasets:
      storage-1:
        database:
          replicaset_uuid: 33333333-3333-3333-3333-333333333333
        instances:
          storage-1-1:
            database:
              instance_uuid: 11111111-1111-1111-1111-111111111111
          storage-1-2: {}
          storage-1-3:
            replication:
              anon: true
            compat:
              skip_replication_names: old
`)

	clusterConfig, err := loadRestoreClusterConfig(path)
	require.NoError(t, err)

	tests := []struct {
		instance string
		want     restore.ConfigIdentity
	}{
		{
			instance: "storage-1-1",
			want: restore.ConfigIdentity{
				ReplicasetName: "storage-1",
				InstanceUUID:   "11111111-1111-1111-1111-111111111111",
				ReplicasetUUID: "33333333-3333-3333-3333-333333333333",
			},
		},
		{
			instance: "storage-1-2",
			want: restore.ConfigIdentity{
				ReplicasetName: "storage-1",
				ReplicasetUUID: "33333333-3333-3333-3333-333333333333",
			},
		},
		{
			instance: "storage-1-3",
			want: restore.ConfigIdentity{
				ReplicasetName: "storage-1",
				ReplicasetUUID: "33333333-3333-3333-3333-333333333333",
				Anon:           true,
				SkipNames:      true,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.instance, func(t *testing.T) {
			identity, err := instanceConfigIdentity(clusterConfig, tc.instance, noEnv)
			require.NoError(t, err)
			assert.Equal(t, tc.want, *identity)
		})
	}
}

// TT_DATABASE_INSTANCE_UUID and TT_DATABASE_REPLICASET_UUID in tt's own
// environment are kept apart from the configuration's UUIDs: they are not the
// instance's environment, and stand in for nothing the configuration says.
func TestInstanceConfigIdentity_KeepsTheEnvironmentApart(t *testing.T) {
	path := writeClusterConfig(t, `groups:
  storages:
    replicasets:
      storage-1:
        instances:
          storage-1-1:
            database:
              instance_uuid: 11111111-1111-1111-1111-111111111111
`)

	clusterConfig, err := loadRestoreClusterConfig(path)
	require.NoError(t, err)

	env := map[string]string{
		"TT_DATABASE_INSTANCE_UUID":   "22222222-2222-2222-2222-222222222222",
		"TT_DATABASE_REPLICASET_UUID": "33333333-3333-3333-3333-333333333333",
	}

	identity, err := instanceConfigIdentity(clusterConfig, "storage-1-1",
		func(key string) string { return env[key] })
	require.NoError(t, err)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", identity.InstanceUUID)
	assert.Empty(t, identity.ReplicasetUUID)
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", identity.EnvInstanceUUID)
	assert.Equal(t, "33333333-3333-3333-3333-333333333333", identity.EnvReplicasetUUID)
}

// tt merges its own TT_ variables into a configuration it reads from a file,
// so a UUID that arrives that way is still counted as the environment's, not
// as one the configuration pins.
func TestInstanceConfigIdentity_CountsAMergedVariableAsTheEnvironments(t *testing.T) {
	path := writeClusterConfig(t, `groups:
  storages:
    replicasets:
      storage-1:
        instances:
          storage-1-1: {}
`)

	t.Setenv("TT_DATABASE_INSTANCE_UUID", "22222222-2222-2222-2222-222222222222")

	clusterConfig, err := loadRestoreClusterConfig(path)
	require.NoError(t, err)

	identity, err := instanceConfigIdentity(clusterConfig, "storage-1-1", os.Getenv)
	require.NoError(t, err)
	assert.Empty(t, identity.InstanceUUID)
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", identity.EnvInstanceUUID)
}

// A key holding a value of the wrong type is a configuration to correct, not
// one to read around.
func TestInstanceConfigIdentity_RejectsAValueOfTheWrongType(t *testing.T) {
	path := writeClusterConfig(t, `groups:
  storages:
    replicasets:
      storage-1:
        instances:
          storage-1-1:
            replication:
              anon: maybe
`)

	clusterConfig, err := loadRestoreClusterConfig(path)
	require.NoError(t, err)

	_, err = instanceConfigIdentity(clusterConfig, "storage-1-1", noEnv)
	require.ErrorIs(t, err, restore.ErrValidation)
	assert.ErrorContains(t, err, "replication.anon")
}
