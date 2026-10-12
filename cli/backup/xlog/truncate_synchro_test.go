package xlog

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"

	xdir "github.com/tarantool/go-xlog/dir"
	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"
)

// A synchronous transaction reaches the journal as two separate records: the
// DML row of the write itself, and the CONFIRM (or ROLLBACK) that resolves it
// only after the quorum acknowledged it (or the timeout gave up on it). A
// recovery point taken between the two therefore cuts the journal at a place
// where the write is in and its resolution is out.
//
// The whole-transaction rule of TruncateAt cannot span the two: they are
// separate transactions in the row stream, so the trimmed journal ends with
// an unconfirmed REQUEST. What an instance restored onto it does with it is
// tarantool's to answer -- the integration twin of this test boots a real
// replicaset on such a chain.

// synchroRow builds a standalone synchro record: the single-row transaction
// a CONFIRM or ROLLBACK is in the stream. The body is opaque to the
// truncation, which copies rows verbatim.
func synchroRow(rowType iproto.Type, lsn int64) format.XRow {
	return format.XRow{
		Type:      rowType,
		ReplicaID: 1,
		LSN:       lsn,
		BodyRaw:   []byte{0x90}, // an empty msgpack map body.
	}
}

// rowKinds reads every row as (replica, lsn, type), so a test can assert not
// only which rows survived a trim but of which kind.
func rowKinds(t *testing.T, path string) [][3]int64 {
	t.Helper()

	r, err := reader.Open(path)
	require.NoError(t, err)

	defer func() { _ = r.Close() }()

	var out [][3]int64
	for row, err := range r.Rows() {
		require.NoError(t, err)
		out = append(out, [3]int64{int64(row.ReplicaID), row.LSN, int64(row.Type)})
	}

	require.True(t, r.SawEOFMarker(), "file %s must end with a valid EOF marker", path)

	return out
}

// The point sits between a synchronous REQUEST and the CONFIRM that resolved
// it: the REQUEST survives, the CONFIRM is dropped, and the journal the
// restored instance replays ends on a write nothing confirmed.
func TestTruncateAt_PointBetweenSyncRequestAndConfirm(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
		{row(t, 1, 10)}, // the sync REQUEST's DML row
		{row(t, 1, 11)}, // the recovery point's row
		{synchroRow(iproto.IPROTO_RAFT_CONFIRM, 12)}, // its CONFIRM, past the point
		{row(t, 1, 13)}, // rows written after
	})

	require.NoError(t, TruncateAt(src, dst, 1, 11))

	confirm := int64(iproto.IPROTO_RAFT_CONFIRM)
	require.Equal(t, [][3]int64{
		{1, 10, int64(iproto.IPROTO_INSERT)},
		{1, 11, int64(iproto.IPROTO_INSERT)},
	}, rowKinds(t, dst))
	require.NotContains(t, rowKinds(t, dst), [3]int64{1, 12, confirm},
		"the CONFIRM row must not survive the trim")
}

// The same shape with the resolution being a ROLLBACK: a synchronous write
// that timed out after the point was taken. The trimmed journal holds the
// write without the rollback that reverted it.
func TestTruncateAt_PointBetweenSyncRequestAndRollback(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
		{row(t, 1, 10)},
		{row(t, 1, 11)},
		{synchroRow(iproto.IPROTO_RAFT_ROLLBACK, 12)},
		{row(t, 1, 13)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 11))

	rollback := int64(iproto.IPROTO_RAFT_ROLLBACK)
	require.Equal(t, [][3]int64{
		{1, 10, int64(iproto.IPROTO_INSERT)},
		{1, 11, int64(iproto.IPROTO_INSERT)},
	}, rowKinds(t, dst))
	require.NotContains(t, rowKinds(t, dst), [3]int64{1, 12, rollback},
		"the ROLLBACK row must not survive the trim")
}

// A point past the CONFIRM keeps it: a synchronous write the point's moment
// already knew as committed travels whole.
func TestTruncateAt_PointPastTheConfirmKeepsIt(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
		{row(t, 1, 10)},
		{synchroRow(iproto.IPROTO_RAFT_CONFIRM, 11)},
		{row(t, 1, 12)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 12))

	confirm := int64(iproto.IPROTO_RAFT_CONFIRM)
	require.Contains(t, rowKinds(t, dst), [3]int64{1, 11, confirm})
}

// The trimmed file stays indexable under its own name: the restored instance
// looks the journal up by the signature the name carries.
func TestTruncateAt_SyncTrimmedFileStaysIndexable(t *testing.T) {
	dir := t.TempDir()

	src := writeChainFile(t, dir, format.VClock{1: 0}, nil, [][]format.XRow{
		{row(t, 1, 10)},
		{row(t, 1, 11)},
		{synchroRow(iproto.IPROTO_RAFT_CONFIRM, 12)},
	})

	outDir := t.TempDir()
	dst := filepath.Join(outDir, filepath.Base(src))
	require.NoError(t, TruncateAt(src, dst, 1, 11))

	_, err := xdir.OpenDir(outDir, format.FiletypeXLOG)
	require.NoError(t, err, "a trimmed file must still index under its own name")
}
