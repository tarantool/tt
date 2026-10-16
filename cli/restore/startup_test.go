package restore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/go-xlog/format"
)

const (
	// schemaSpaceID is box.space._schema.id: where a synthetic snapshot
	// records its replicaset's UUID and name.
	schemaSpaceID = 272
	// startupReplicaset is the replicaset name the synthetic snapshots of
	// these tests record, and the configuration puts the instance in.
	startupReplicaset = "storage-1"
)

// schemaRow builds one _schema row of a synthetic snapshot.
func schemaRow(t *testing.T, lsn int64, key, value string) format.XRow {
	t.Helper()

	body, err := msgpack.Marshal(map[uint64]any{
		uint64(iproto.IPROTO_SPACE_ID): schemaSpaceID,
		uint64(iproto.IPROTO_TUPLE):    []any{key, value},
	})
	require.NoError(t, err)

	return format.XRow{
		Type:      iproto.IPROTO_INSERT,
		ReplicaID: 1,
		LSN:       lsn,
		BodyRaw:   body,
	}
}

// startupChain packs a one-archive chain whose snapshot at {1: 10} names the
// replicaset (when replicasetName is not empty) and carries members, and
// whose journal after it carries the rows given.
func startupChain(
	t *testing.T,
	replicasetName string,
	members []clusterMember,
	journal []format.XRow,
) string {
	t.Helper()

	txs := [][]format.XRow{{schemaRow(t, 1, "replicaset_uuid", replicasetUUID)}}
	if replicasetName != "" {
		txs = append(txs, []format.XRow{schemaRow(t, 2, "replicaset_name", replicasetName)})
	}

	for i, member := range members {
		txs = append(txs, []format.XRow{clusterRow(t, int64(i+3), member)})
	}

	src := t.TempDir()
	snap := writeJournal(t, src, format.FiletypeSNAP, format.VClock{1: 10}, nil, txs)

	var journalTxs [][]format.XRow
	for _, row := range journal {
		journalTxs = append(journalTxs, []format.XRow{row})
	}

	wal := writeXlog(t, src, format.VClock{1: 10}, nil, journalTxs)

	return packArchive(t, filepath.Join(t.TempDir(), "startup.tar.zst"), snap, wal)
}

// startupOpts restores the chain onto the replica, the way a per-instance
// restore does: the replica's own UUID, its own name, and the configuration
// given.
func startupOpts(t *testing.T, archive string, config *ConfigIdentity) (ApplyOpts, string) {
	t.Helper()

	workDir := filepath.Join(t.TempDir(), "storage-1-2")

	return ApplyOpts{
		Archives:     []string{archive},
		Layout:       FlatLayout(workDir),
		InstanceName: "storage-1-2",
		PatchUUID:    replicaUUID,
		Config:       config,
	}, workDir
}

// The bootstrap snapshot case: the snapshot holds the leader alone, and the
// replica registered -- under its name -- only in the journal after it.
// The membership check is satisfied, the replay ends with the replica named;
// Tarantool still refuses to start it, because it reads the name from the
// snapshot alone. The restore is refused, naming the ways out.
func TestApply_RefusesAnInstanceTheSnapshotHasNoNameFor(t *testing.T) {
	archive := startupChain(t, startupReplicaset,
		[]clusterMember{{1, masterUUID, "storage-1-1"}},
		[]format.XRow{clusterRow(t, 11, clusterMember{2, replicaUUID, "storage-1-2"})})

	opts, workDir := startupOpts(t, archive,
		&ConfigIdentity{ReplicasetName: startupReplicaset})

	_, err := Apply(opts)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrValidation,
		"the directories are rebuilt by now: nothing of a previous attempt is kept")

	require.Contains(t, err.Error(), `instance "storage-1-2" would not start`)
	require.Contains(t, err.Error(), "records no name for its instance uuid "+replicaUUID)
	require.Contains(t, err.Error(), `do register the uuid as "storage-1-2"`,
		"the refusal says the replay has the name, and why that is not enough")
	require.Contains(t, err.Error(), "TT_DATABASE_INSTANCE_UUID="+replicaUUID)
	require.Contains(t, err.Error(), "box.snapshot()")

	require.NoFileExists(t, StatePath(workDir), "a refused restore leaves no marker")
}

// tt's own environment is not the instance's: a UUID found there is no proof
// the instance starts, and the instance's own UUID only turns the refusal into
// a warning. Another instance's UUID is no reason to let the restore pass.
func TestApply_WarnsOnAUUIDFromTheEnvironmentOfTT(t *testing.T) {
	archive := startupChain(t, startupReplicaset,
		[]clusterMember{{1, masterUUID, "storage-1-1"}},
		[]format.XRow{clusterRow(t, 11, clusterMember{2, replicaUUID, "storage-1-2"})})

	t.Run("its own uuid", func(t *testing.T) {
		opts, workDir := startupOpts(t, archive, &ConfigIdentity{
			ReplicasetName:  startupReplicaset,
			EnvInstanceUUID: replicaUUID,
		})

		_, err := Apply(opts)
		require.NoError(t, err)
		require.FileExists(t, StatePath(workDir))
	})

	t.Run("another uuid", func(t *testing.T) {
		other := "44444444-4444-4444-4444-444444444444"

		opts, workDir := startupOpts(t, archive, &ConfigIdentity{
			ReplicasetName:  startupReplicaset,
			EnvInstanceUUID: other,
		})

		_, err := Apply(opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), "TT_DATABASE_INSTANCE_UUID in tt's environment is "+other)
		require.NoFileExists(t, StatePath(workDir))
	})
}

// What the configuration pins decides it: the replica's own UUID is what
// Tarantool needs in place of the missing name, and any other UUID is a
// mismatch it refuses on its own.
func TestApply_TakesTheConfiguredUUIDInPlaceOfTheName(t *testing.T) {
	archive := startupChain(t, startupReplicaset,
		[]clusterMember{{1, masterUUID, "storage-1-1"}},
		[]format.XRow{clusterRow(t, 11, clusterMember{2, replicaUUID, "storage-1-2"})})

	t.Run("accepts the instance's own uuid", func(t *testing.T) {
		opts, workDir := startupOpts(t, archive, &ConfigIdentity{
			ReplicasetName: startupReplicaset,
			InstanceUUID:   replicaUUID,
		})

		_, err := Apply(opts)
		require.NoError(t, err)
		require.FileExists(t, StatePath(workDir))
	})

	t.Run("refuses another uuid", func(t *testing.T) {
		other := "44444444-4444-4444-4444-444444444444"

		opts, workDir := startupOpts(t, archive, &ConfigIdentity{
			ReplicasetName: startupReplicaset,
			InstanceUUID:   other,
		})

		_, err := Apply(opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), "pins database.instance_uuid "+other)
		require.NoFileExists(t, StatePath(workDir))
	})
}

// A row that is there without a name is no better for starting than no row:
// the replay offers no name either, and the refusal says so.
func TestApply_RefusesANamelessRowWithoutAConfiguredUUID(t *testing.T) {
	archive := startupChain(t, startupReplicaset,
		[]clusterMember{{1, masterUUID, "storage-1-1"}, {2, replicaUUID, ""}}, nil)

	opts, _ := startupOpts(t, archive, &ConfigIdentity{ReplicasetName: startupReplicaset})

	_, err := Apply(opts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "register the uuid without a name")
}

// A snapshot that names the instance is all Tarantool needs, configuration
// UUIDs or not -- the case of a full backup taken after box.snapshot().
func TestApply_AcceptsAnInstanceTheSnapshotNames(t *testing.T) {
	archive := startupChain(t, startupReplicaset,
		[]clusterMember{{1, masterUUID, "storage-1-1"}, {2, replicaUUID, "storage-1-2"}}, nil)

	opts, workDir := startupOpts(t, archive, &ConfigIdentity{ReplicasetName: startupReplicaset})

	_, err := Apply(opts)
	require.NoError(t, err)
	require.FileExists(t, StatePath(workDir))
}

// Under the two settings Tarantool requires no name, neither does the check.
func TestApply_RequiresNoNameWhereTarantoolDoesNot(t *testing.T) {
	archive := startupChain(t, startupReplicaset,
		[]clusterMember{{1, masterUUID, "storage-1-1"}},
		[]format.XRow{clusterRow(t, 11, clusterMember{2, replicaUUID, ""})})

	for name, config := range map[string]*ConfigIdentity{
		"an anonymous replica": {ReplicasetName: startupReplicaset, Anon: true},
		"names skipped by compat": {
			ReplicasetName: startupReplicaset,
			SkipNames:      true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			opts, _ := startupOpts(t, archive, config)

			_, err := Apply(opts)
			require.NoError(t, err)
		})
	}
}

// Without a configuration the instance may never be started from one, and a
// box.cfg() of its own asks for no name: the finding is a warning, and the
// restore completes.
func TestApply_OnlyWarnsWithoutAConfiguration(t *testing.T) {
	archive := startupChain(t, startupReplicaset,
		[]clusterMember{{1, masterUUID, "storage-1-1"}},
		[]format.XRow{clusterRow(t, 11, clusterMember{2, replicaUUID, "storage-1-2"})})

	opts, workDir := startupOpts(t, archive, nil)

	_, err := Apply(opts)
	require.NoError(t, err)
	require.FileExists(t, StatePath(workDir))
}

// The replicaset is held to the same check as the instance: its UUID and name
// against the configuration's, and a snapshot without a name needs the UUID.
func TestApply_ChecksTheReplicasetTheWayTarantoolDoes(t *testing.T) {
	named := []clusterMember{{1, masterUUID, "storage-1-1"}, {2, replicaUUID, "storage-1-2"}}

	t.Run("refuses another replicaset uuid", func(t *testing.T) {
		other := "44444444-4444-4444-4444-444444444444"
		archive := startupChain(t, startupReplicaset, named, nil)

		opts, _ := startupOpts(t, archive, &ConfigIdentity{
			ReplicasetName: startupReplicaset,
			ReplicasetUUID: other,
		})

		_, err := Apply(opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), "pins database.replicaset_uuid "+other)
	})

	t.Run("refuses another replicaset name", func(t *testing.T) {
		archive := startupChain(t, startupReplicaset, named, nil)

		opts, _ := startupOpts(t, archive, &ConfigIdentity{ReplicasetName: "storage-2"})

		_, err := Apply(opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), `names the replicaset "storage-1"`)
	})

	t.Run("refuses an unnamed replicaset without a configured uuid", func(t *testing.T) {
		archive := startupChain(t, "", named, nil)

		opts, _ := startupOpts(t, archive, &ConfigIdentity{ReplicasetName: startupReplicaset})

		_, err := Apply(opts)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no name for its replicaset uuid "+replicasetUUID)
	})

	t.Run("accepts an unnamed replicaset with its uuid configured", func(t *testing.T) {
		archive := startupChain(t, "", named, nil)

		opts, _ := startupOpts(t, archive, &ConfigIdentity{
			ReplicasetName: startupReplicaset,
			ReplicasetUUID: replicasetUUID,
		})

		_, err := Apply(opts)
		require.NoError(t, err)
	})
}
