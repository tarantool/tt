package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"
)

// The membership a fragment records used to be read off the frozen snapshot
// alone: a member registering between the checkpoint and the freeze has its
// registration row inside the backed-up WAL, so the fold restore apply runs
// knows it while a snapshot-only fragment does not -- the member's node went
// to rejoin over data that boots it whole, and a uuid replaced after the
// checkpoint left the manifest carrying one the apply refuses. The fragment
// now folds the backed-up journals into the snapshot and clamps the fold at
// the frozen vclock: a registration inside the frozen range is recorded, a
// row past the freeze is not, and the live _cluster is never read, so the
// eval order cannot matter; the instance info below still reports a
// post-freeze member, and the fragment must ignore it.
func TestStart_RecordsTheMembershipTheFrozenRangeCarries(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())

	walDir := t.TempDir()

	const (
		uuidB = "aaaaaaaa-0000-0000-0000-000000000002"
		uuidC = "aaaaaaaa-0000-0000-0000-000000000003"
		uuidD = "aaaaaaaa-0000-0000-0000-000000000004"
	)

	// The checkpoint: two members, the backed-up node and one replica,
	// frozen at {1: 1500}. The third member registers after it and before
	// the freeze: its row (replica 1, lsn 1501) is inside the frozen range
	// the backed-up WAL carries. The fourth registers past the freeze (lsn
	// 1503 > 1502): inside the WAL's bytes, outside the range.
	writeSnapshot(t, filepath.Join(walDir, "00000000000000001500.snap"),
		clusterTuple(1, testInstanceUUID, "storage-001-a"),
		clusterTuple(2, uuidB, "storage-001-b"),
	)
	writeJournal(t, filepath.Join(walDir, "00000000000000001500.xlog"),
		format.XRow{
			Type:      iproto.IPROTO_INSERT,
			ReplicaID: 1,
			LSN:       1501,
			BodyRaw:   clusterRowBody(t, clusterTuple(3, uuidC, "storage-001-c")),
		},
		format.XRow{
			Type:      iproto.IPROTO_INSERT,
			ReplicaID: 1,
			LSN:       1503,
			BodyRaw:   clusterRowBody(t, clusterTuple(4, uuidD, "storage-001-d")),
		},
	)

	// The frozen range ends at {1: 1502}: the vclock box.backup.start froze.
	info := infoMap(walFiles, nil, Vclock{1: 1502}, nil)

	// What a live read would return after the freeze: the fourth member is
	// there too. The flow never reads it.
	inst := instanceMap("storage-001-a", walDir, "")

	inst["cluster"] = []any{
		[]any{uint64(1), testInstanceUUID, "storage-001-a"},
		[]any{uint64(2), uuidB, "storage-001-b"},
		[]any{uint64(3), uuidC, "storage-001-c"},
		[]any{uint64(4), uuidD, "storage-001-d"},
	}

	mock := &mockEvaler{queue: startQueue(info, inst)}

	archivePath, err := Start(mock, BackupStartOpts{BackupID: "20260312T120000Z"})
	require.NoError(t, err)

	// No live _cluster read happens at all: the membership cannot race a
	// read that does not exist.
	require.Len(t, mock.exprs, 4, mock.exprs)

	for i, expr := range mock.exprs {
		require.NotContains(t, expr, "box.space._cluster",
			"eval %d must not read the live _cluster", i)
	}

	// The fragment: the frozen vclock_end beside the membership the frozen
	// range carries.
	fragmentData, err := os.ReadFile(fragmentPathFor(archivePath))
	require.NoError(t, err)

	var fragment Fragment

	require.NoError(t, json.Unmarshal(fragmentData, &fragment))

	require.Equal(t, Vclock{1: 1502}, fragment.VclockEnd, fragment)

	require.Equal(t, uint32(1), fragment.InstanceID,
		"the master's own row, read from the same fold")

	require.Equal(t, []ClusterMember{
		{ID: 2, UUID: uuidB, Name: "storage-001-b"},
		{ID: 3, UUID: uuidC, Name: "storage-001-c"},
	}, fragment.Members, "the registration inside the frozen range is "+
		"recorded; the row past the freeze and the live read are not")
}
