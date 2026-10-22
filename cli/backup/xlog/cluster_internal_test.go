package xlog

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/go-xlog/format"
)

// clusterBody builds a DML body naming the _cluster space and carrying the
// tuple given, the way a snapshot writes its membership rows.
func clusterBody(t *testing.T, tuple []any) []byte {
	t.Helper()

	body, err := msgpack.Marshal(map[uint64]any{
		uint64(iproto.IPROTO_SPACE_ID): clusterSpaceID,
		uint64(iproto.IPROTO_TUPLE):    tuple,
	})
	require.NoError(t, err)

	return body
}

// clusterTx is one _cluster row as a single-row transaction.
func clusterTx(t *testing.T, lsn int64, tuple []any) []format.XRow {
	t.Helper()

	return []format.XRow{{
		Type:      iproto.IPROTO_INSERT,
		ReplicaID: 1,
		LSN:       lsn,
		BodyRaw:   clusterBody(t, tuple),
	}}
}

func writeClusterSnap(t *testing.T, txs ...[]format.XRow) string {
	t.Helper()

	path := tmpPath(t, "00000000000000000000.snap")
	writeXlog(t, path, snapMeta(t, testUUID, format.VClock{1: 0}), txs)

	return path
}

// The membership a snapshot records comes back one member per _cluster row,
// with the name of a member that carries one and the empty name of one that
// does not -- whether the field is absent or nil, which is both ways
// Tarantool leaves a member unnamed.
func TestReadCluster_ReadsTheMembership(t *testing.T) {
	path := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
		clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222"}),
		clusterTx(t, 3, []any{3, "33333333-3333-3333-3333-333333333333", nil}),
	)

	members, err := ReadCluster(path)
	require.NoError(t, err)

	require.Equal(t, []ClusterMember{
		{ID: 1, UUID: "11111111-1111-1111-1111-111111111111", Name: "master"},
		{ID: 2, UUID: "22222222-2222-2222-2222-222222222222"},
		{ID: 3, UUID: "33333333-3333-3333-3333-333333333333"},
	}, members)
}

// The rows a snapshot holds beside _cluster -- schema, user data, whole
// spaces of them -- are skipped without their tuples being decoded.
func TestReadCluster_SkipsTheOtherSpaces(t *testing.T) {
	body, err := msgpack.Marshal(map[uint64]any{
		uint64(iproto.IPROTO_SPACE_ID): uint64(512),
		uint64(iproto.IPROTO_TUPLE):    []any{"not", "a", "member"},
	})
	require.NoError(t, err)

	otherSpaceTx := []format.XRow{{
		Type:      iproto.IPROTO_INSERT,
		ReplicaID: 1,
		LSN:       1,
		BodyRaw:   body,
	}}

	path := writeClusterSnap(t,
		otherSpaceTx,
		clusterTx(t, 2, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
	)

	members, err := ReadCluster(path)
	require.NoError(t, err)

	require.Equal(t, []ClusterMember{
		{ID: 1, UUID: "11111111-1111-1111-1111-111111111111", Name: "master"},
	}, members)
}

// A snapshot without _cluster rows -- one a test wrote by hand, or anything
// but a Tarantool checkpoint -- reports an empty membership rather than an
// error: what the caller refuses on is its own decision.
func TestReadCluster_EmptyMembership(t *testing.T) {
	path := writeClusterSnap(t)

	members, err := ReadCluster(path)
	require.NoError(t, err)
	require.Empty(t, members)
}

// A _cluster tuple that is not [id, uuid, name?] is a corrupt snapshot, and
// is reported as a read failure rather than skipped: a membership silently
// short a member is a check that passes for the wrong reason.
func TestReadCluster_RefusesATupleThatIsNotAMember(t *testing.T) {
	path := writeClusterSnap(t,
		clusterTx(t, 1, []any{"just-a-string"}),
	)

	_, err := ReadCluster(path)
	require.ErrorContains(t, err, "not a _cluster row")
}

// clusterUpdateBody builds a _cluster update's body the way Tarantool packs
// it into the journal: the key of the member and the operations under the
// tuple key.
func clusterUpdateBody(t *testing.T, memberID uint32, name string) []byte {
	t.Helper()

	body, err := msgpack.Marshal(map[uint64]any{
		uint64(iproto.IPROTO_SPACE_ID): clusterSpaceID,
		uint64(iproto.IPROTO_KEY):      []any{memberID},
		uint64(iproto.IPROTO_TUPLE):    []any{[]any{"=", 3, name}},
	})
	require.NoError(t, err)

	return body
}

// A member the snapshot holds nameless and a journal names: the fold ends
// with the name, the way a replay of the two files does.
func TestMembershipAt_AppliesANameUpdate(t *testing.T) {
	snap := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
		clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222"}),
	)
	wal := tmpPath(t, "00000000000000000002.xlog")
	writeXlog(t, wal, xlogMeta(t, format.VClock{1: 2}, nil), [][]format.XRow{{
		{
			Type:      iproto.IPROTO_UPDATE,
			ReplicaID: 1,
			LSN:       3,
			BodyRaw:   clusterUpdateBody(t, 2, "renamed"),
		},
	}})

	members, err := MembershipAt(snap, []string{wal})
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "renamed", members[1].Name)
}

// The same fold against a journal a live Tarantool 3.9 wrote: the bytes the
// synthetic test builds by hand are the bytes the server produces.
func TestMembershipAt_FoldsANameUpdateFromALiveWAL(t *testing.T) {
	snap := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
		clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222"}),
	)

	members, err := MembershipAt(snap, []string{"testdata/cluster_update_name.xlog"})
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "renamed", members[1].Name)
}

// MembershipUpTo answers: what does the frozen range end with? A member
// registered between the checkpoint and the freeze -- its registration row
// in the journal, at an LSN the frozen vclock covers -- is inside the range
// the fold answers for, whatever side of the checkpoint it registered on.
func TestMembershipUpTo_KeepsARegistrationInsideTheRange(t *testing.T) {
	snap := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
		clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222"}),
	)
	wal := tmpPath(t, "00000000000000000002.xlog")
	writeXlog(t, wal, xlogMeta(t, format.VClock{1: 2}, nil), [][]format.XRow{{
		{
			Type:      iproto.IPROTO_INSERT,
			ReplicaID: 2,
			LSN:       5,
			BodyRaw:   clusterBody(t, []any{3, "33333333-3333-3333-3333-333333333333", "late"}),
		},
	}})

	members, err := MembershipUpTo(snap, []string{wal}, format.VClock{1: 6, 2: 5})
	require.NoError(t, err)

	require.Equal(t, []ClusterMember{
		{ID: 1, UUID: "11111111-1111-1111-1111-111111111111", Name: "master"},
		{ID: 2, UUID: "22222222-2222-2222-2222-222222222222"},
		{ID: 3, UUID: "33333333-3333-3333-3333-333333333333", Name: "late"},
	}, members)
}

// The same registration clamped short of its row: the row lies past the
// freeze, and the fold drops it.
func TestMembershipUpTo_DropsARowPastTheClamp(t *testing.T) {
	snap := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
		clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222"}),
	)
	wal := tmpPath(t, "00000000000000000002.xlog")
	writeXlog(t, wal, xlogMeta(t, format.VClock{1: 2}, nil), [][]format.XRow{{
		{
			Type:      iproto.IPROTO_INSERT,
			ReplicaID: 2,
			LSN:       5,
			BodyRaw:   clusterBody(t, []any{3, "33333333-3333-3333-3333-333333333333", "late"}),
		},
	}})

	members, err := MembershipUpTo(snap, []string{wal}, format.VClock{1: 6, 2: 4})
	require.NoError(t, err)

	require.Equal(t, []ClusterMember{
		{ID: 1, UUID: "11111111-1111-1111-1111-111111111111", Name: "master"},
		{ID: 2, UUID: "22222222-2222-2222-2222-222222222222"},
	}, members)
}

// A replica id absent from the clamp counts as 0: that replica wrote nothing
// inside the frozen range, so even the first row it ever wrote lies past it.
func TestMembershipUpTo_DropsAReplicaTheClampNeverSaw(t *testing.T) {
	snap := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
	)
	wal := tmpPath(t, "00000000000000000001.xlog")
	writeXlog(t, wal, xlogMeta(t, format.VClock{1: 1}, nil), [][]format.XRow{{
		{
			Type:      iproto.IPROTO_INSERT,
			ReplicaID: 2,
			LSN:       1,
			BodyRaw:   clusterBody(t, []any{2, "22222222-2222-2222-2222-222222222222"}),
		},
	}})

	members, err := MembershipUpTo(snap, []string{wal}, format.VClock{1: 6})
	require.NoError(t, err)

	require.Equal(t, []ClusterMember{
		{ID: 1, UUID: "11111111-1111-1111-1111-111111111111", Name: "master"},
	}, members)
}

// The clamp against a journal a live Tarantool 3.9 wrote: the rename at lsn
// 3 sits inside a clamp of {1: 3} and past a clamp of {1: 2}, on the real
// bytes.
func TestMembershipUpTo_ClampsALiveWAL(t *testing.T) {
	snap := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
		clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222"}),
	)

	inside, err := MembershipUpTo(
		snap, []string{"testdata/cluster_update_name.xlog"}, format.VClock{1: 3},
	)
	require.NoError(t, err)
	require.Len(t, inside, 2)
	require.Equal(t, "renamed", inside[1].Name)

	past, err := MembershipUpTo(
		snap, []string{"testdata/cluster_update_name.xlog"}, format.VClock{1: 2},
	)
	require.NoError(t, err)
	require.Len(t, past, 2)
	require.Empty(t, past[1].Name, "the row past the clamp is not applied")
}

// clusterUpdateOpsBody packs a _cluster update's operations under the
// dedicated operations key: the branch a live 3.9 WAL does not take (it
// writes them under the tuple key), pinned so a future layout change cannot
// silently lose it.
func clusterUpdateOpsBody(t *testing.T, memberID uint32, name string) []byte {
	t.Helper()

	body, err := msgpack.Marshal(map[uint64]any{
		uint64(iproto.IPROTO_SPACE_ID): clusterSpaceID,
		uint64(iproto.IPROTO_KEY):      []any{memberID},
		uint64(iproto.IPROTO_OPS):      []any{[]any{"=", 3, name}},
	})
	require.NoError(t, err)

	return body
}

// The same fold through the operations key: whichever of the two keys the
// journal carries, the name lands.
func TestMembershipAt_AppliesANameUpdateFromTheOperationsKey(t *testing.T) {
	snap := writeClusterSnap(t,
		clusterTx(t, 1, []any{1, "11111111-1111-1111-1111-111111111111", "master"}),
		clusterTx(t, 2, []any{2, "22222222-2222-2222-2222-222222222222"}),
	)
	wal := tmpPath(t, "00000000000000000002.xlog")
	writeXlog(t, wal, xlogMeta(t, format.VClock{1: 2}, nil), [][]format.XRow{{
		{
			Type:      iproto.IPROTO_UPDATE,
			ReplicaID: 1,
			LSN:       3,
			BodyRaw:   clusterUpdateOpsBody(t, 2, "renamed"),
		},
	}})

	members, err := MembershipAt(snap, []string{wal})
	require.NoError(t, err)
	require.Len(t, members, 2)
	require.Equal(t, "renamed", members[1].Name)
}
