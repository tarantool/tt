package xlog

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-xlog/format"
)

// The point is inclusive: LSN == lsn is kept, everything above dropped.
func TestTruncateAt_InclusivePoint(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 5}, nil), [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2)},
		{row(t, 1, 3)},
		{row(t, 1, 4)},
		{row(t, 1, 5)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 3))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}, {1, 3}}, readAllRows(t, dst))
}

// The output VClock is clamped to the point so the signature matches content.
func TestTruncateAt_ClampsVClock(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 5}, nil), [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2)},
		{row(t, 1, 3)},
		{row(t, 1, 4)},
		{row(t, 1, 5)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 3))

	require.Equal(t, int64(3), readMeta(t, dst).VClock[1])
}

// A multi-row tx straddling the point is kept whole — truncation never splits
// a transaction.
func TestTruncateAt_MultiRowTxAcrossPointKeptWhole(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 5}, nil), [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2), row(t, 1, 3), row(t, 1, 4)},
		{row(t, 1, 5)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 3))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}, {1, 3}, {1, 4}}, readAllRows(t, dst))
}

// A transaction entirely above the point is dropped whole.
func TestTruncateAt_TxWhollyAbovePointDropped(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 4}, nil), [][]format.XRow{
		{row(t, 1, 1), row(t, 1, 2)},
		{row(t, 1, 3), row(t, 1, 4)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 2))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}}, readAllRows(t, dst))
}

// Truncating on replica 1 keeps replica-2 rows riding along in kept txs, and
// each replica's high-water reflects only the written rows.
func TestTruncateAt_MultiReplica(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 3, 2: 2}, nil), [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 2, 1), row(t, 1, 2)},
		{row(t, 2, 2), row(t, 1, 3)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 2))

	require.Equal(t, []rowKey{{1, 1}, {2, 1}, {1, 2}}, readAllRows(t, dst))

	m := readMeta(t, dst)
	require.Equal(t, int64(2), m.VClock[1])
	require.Equal(t, int64(1), m.VClock[2])
}

// Point on the last row: output equals input.
func TestTruncateAt_PointOnLastRow(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 3}, nil), [][]format.XRow{
		{row(t, 1, 1)}, {row(t, 1, 2)}, {row(t, 1, 3)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 3))

	require.Equal(t, readAllRows(t, src), readAllRows(t, dst))
}

// An empty xlog truncates to a valid, empty, EOF-terminated file.
func TestTruncateAt_EmptyXlog(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{}, nil), nil)

	require.NoError(t, TruncateAt(src, dst, 1, 10))

	require.Empty(t, readAllRows(t, dst))
}

// The output always ends with a valid EOF marker.
func TestTruncateAt_OutputIsFinalized(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 4}, nil), [][]format.XRow{
		{row(t, 1, 1)}, {row(t, 1, 2)}, {row(t, 1, 3)}, {row(t, 1, 4)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 2))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}}, readAllRows(t, dst))
}
