package restore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The chain of testdata/bootstrap_chain was backed up by Tarantool 3.8.1 from
// the master storage-1-3 of a three-member replicaset storage-1 whose only
// snapshot was the bootstrap one: its _cluster holds storage-1-3 alone, and
// storage-1-1 and storage-1-2 registered -- named -- in the journal after it.
// The recovery point is {1: 346}. Restored onto a replica, it is the data
// Tarantool refused to start in the field.
const (
	bootstrapChainDir   = "testdata/bootstrap_chain"
	bootstrapMaster     = "storage-1-3"
	bootstrapMasterID   = "17e20e3a-062b-44e1-a957-256e90624d91"
	bootstrapReplica    = "storage-1-1"
	bootstrapReplicaID  = "905bb948-7485-4b36-8954-cc73d3c63f81"
	bootstrapReplicaset = "storage-1"
	bootstrapPointLSN   = 346
)

// bootstrapChainOpts restores the bootstrap chain onto the instance given,
// at the point, with the configuration given.
func bootstrapChainOpts(
	t *testing.T,
	instance, instanceUUID string,
	config *ConfigIdentity,
) (ApplyOpts, string) {
	t.Helper()

	workDir := filepath.Join(t.TempDir(), instance)

	return ApplyOpts{
		Archives: []string{
			filepath.Join(bootstrapChainDir, "full.tar.zst"),
			filepath.Join(bootstrapChainDir, "incremental.tar.zst"),
		},
		Layout:       FlatLayout(workDir),
		InstanceName: instance,
		Point:        &Point{ReplicaID: 1, LSN: bootstrapPointLSN},
		PatchUUID:    instanceUUID,
		Config:       config,
	}, workDir
}

// The replica is refused: the snapshot does not name it, while the journal
// after it does -- which the refusal says, along with the ways out.
func TestApply_RefusesTheReplicaOfABootstrapSnapshot(t *testing.T) {
	opts, workDir := bootstrapChainOpts(t, bootstrapReplica, bootstrapReplicaID,
		&ConfigIdentity{ReplicasetName: bootstrapReplicaset})

	_, err := Apply(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no name for its instance uuid "+bootstrapReplicaID)
	require.Contains(t, err.Error(), bootstrapMasterID+` "`+bootstrapMaster+`"`,
		"the refusal shows the _cluster the snapshot does hold")
	require.Contains(t, err.Error(), `do register the uuid as "`+bootstrapReplica+`"`)
	require.Contains(t, err.Error(), "TT_DATABASE_INSTANCE_UUID="+bootstrapReplicaID)
	require.Contains(t, err.Error(), "box.snapshot()")
	require.NoFileExists(t, StatePath(workDir))
}

// The master the backup was taken on is named by its own snapshot.
func TestApply_AcceptsTheMasterOfABootstrapSnapshot(t *testing.T) {
	opts, workDir := bootstrapChainOpts(t, bootstrapMaster, bootstrapMasterID,
		&ConfigIdentity{ReplicasetName: bootstrapReplicaset})

	_, err := Apply(opts)
	require.NoError(t, err)
	require.FileExists(t, StatePath(workDir))
}

// What lets the replica through: its UUID in the configuration; its UUID in
// tt's environment, with a warning; no configuration at all, with a warning.
func TestApply_LetsTheReplicaOfABootstrapSnapshotThrough(t *testing.T) {
	for name, config := range map[string]*ConfigIdentity{
		"its uuid in the configuration": {
			ReplicasetName: bootstrapReplicaset,
			InstanceUUID:   bootstrapReplicaID,
		},
		"its uuid in tt's environment": {
			ReplicasetName:  bootstrapReplicaset,
			EnvInstanceUUID: bootstrapReplicaID,
		},
		"no configuration": nil,
	} {
		t.Run(name, func(t *testing.T) {
			opts, workDir := bootstrapChainOpts(t, bootstrapReplica, bootstrapReplicaID, config)

			_, err := Apply(opts)
			require.NoError(t, err)
			require.FileExists(t, StatePath(workDir))
		})
	}
}
