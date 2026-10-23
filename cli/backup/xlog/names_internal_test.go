package xlog

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/go-xlog/format"
)

// schemaTx is one _schema row as a single-row transaction.
func schemaTx(t *testing.T, lsn int64, tuple []any) []format.XRow {
	t.Helper()

	body, err := msgpack.Marshal(map[uint64]any{
		uint64(iproto.IPROTO_SPACE_ID): schemaSpaceID,
		uint64(iproto.IPROTO_TUPLE):    tuple,
	})
	require.NoError(t, err)

	return []format.XRow{{
		Type:      iproto.IPROTO_INSERT,
		ReplicaID: 1,
		LSN:       lsn,
		BodyRaw:   body,
	}}
}

// The names come from the snapshot the way Tarantool reads them: the header's
// UUID, the name of the _cluster row carrying it, and the replicaset's UUID
// and name out of _schema -- whose other keys, of any type, are left alone.
func TestReadSnapshotNames_ReadsWhatTheConfigChecks(t *testing.T) {
	path := writeClusterSnap(t,
		schemaTx(t, 1, []any{"max_id", 512}),
		schemaTx(t, 2, []any{"replicaset_name", "storage-1"}),
		schemaTx(t, 3, []any{"replicaset_uuid", "44444444-4444-4444-4444-444444444444"}),
		schemaTx(t, 4, []any{"version", 3, 8, 1}),
		clusterTx(t, 5, []any{1, testUUID, "storage-1-1"}),
		clusterTx(t, 6, []any{2, "22222222-2222-2222-2222-222222222222", "storage-1-2"}),
	)

	names, err := ReadSnapshotNames(path)
	require.NoError(t, err)

	require.Equal(t, SnapshotNames{
		InstanceUUID:   testUUID,
		InstanceName:   "storage-1-1",
		ReplicasetUUID: "44444444-4444-4444-4444-444444444444",
		ReplicasetName: "storage-1",
		Members: []ClusterMember{
			{ID: 1, UUID: testUUID, Name: "storage-1-1"},
			{ID: 2, UUID: "22222222-2222-2222-2222-222222222222", Name: "storage-1-2"},
		},
	}, names)
}

// The instance has no name in the snapshot both when its row is there
// without one and when no row carries its UUID at all.
func TestReadSnapshotNames_NoNameForTheHeadersUUID(t *testing.T) {
	t.Run("a row without a name", func(t *testing.T) {
		path := writeClusterSnap(t,
			clusterTx(t, 1, []any{1, testUUID}),
			clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222", "other"}),
		)

		names, err := ReadSnapshotNames(path)
		require.NoError(t, err)
		require.Empty(t, names.InstanceName)
		require.Len(t, names.Members, 2)
	})

	t.Run("no row at all", func(t *testing.T) {
		path := writeClusterSnap(t,
			clusterTx(t, 1, []any{2, "22222222-2222-2222-2222-222222222222", "other"}),
		)

		names, err := ReadSnapshotNames(path)
		require.NoError(t, err)
		require.Empty(t, names.InstanceName)
	})
}

// Tarantool before 2.11 stored the replicaset UUID under the "cluster" key.
func TestReadSnapshotNames_ReadsTheOldReplicasetUUIDKey(t *testing.T) {
	path := writeClusterSnap(t,
		schemaTx(t, 1, []any{"cluster", "44444444-4444-4444-4444-444444444444"}),
		clusterTx(t, 2, []any{1, testUUID}),
	)

	names, err := ReadSnapshotNames(path)
	require.NoError(t, err)
	require.Equal(t, "44444444-4444-4444-4444-444444444444", names.ReplicasetUUID)
	require.Empty(t, names.ReplicasetName)
}

// Snapshots Tarantool 3.8.1 wrote. The first is a bootstrap snapshot restored
// onto a replica, its header stamped with the replica's UUID: _cluster holds
// only the bootstrap leader, because the replicas registered in the journal
// after it -- the snapshot Tarantool refuses to start a replica on. The second
// was taken after the replicas registered, and names every one of them.
func TestReadSnapshotNames_RealSnapshots(t *testing.T) {
	missing, err := ReadSnapshotNames("testdata/names_member_missing.snap")
	require.NoError(t, err)
	require.Equal(t, "905bb948-7485-4b36-8954-cc73d3c63f81", missing.InstanceUUID)
	require.Empty(t, missing.InstanceName)
	require.Equal(t, "storage-1", missing.ReplicasetName)
	require.NotEmpty(t, missing.ReplicasetUUID)
	require.Equal(t, []ClusterMember{
		{ID: 1, UUID: "17e20e3a-062b-44e1-a957-256e90624d91", Name: "storage-1-3"},
	}, missing.Members)

	named, err := ReadSnapshotNames("testdata/names_member_named.snap")
	require.NoError(t, err)
	require.Equal(t, "98265f79-f7bf-4c17-b63f-e215a3ebce13", named.InstanceUUID)
	require.Equal(t, "storage-1-1", named.InstanceName)
	require.Equal(t, "storage-1", named.ReplicasetName)
	require.Len(t, named.Members, 3)
}
