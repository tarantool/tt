package xlog

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	xdir "github.com/tarantool/go-xlog/dir"
	"github.com/tarantool/go-xlog/format"
)

// The point is inclusive: LSN == lsn is kept, everything above dropped.
func TestTruncateAt_InclusivePoint(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2)},
		{row(t, 1, 3)},
		{row(t, 1, 4)},
		{row(t, 1, 5)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 3))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}, {1, 3}}, readAllRows(t, dst))
}

// The header is carried over untouched. VClock is where the file starts and
// the file name is its signature, so a dropped tail must not move it —
// see TruncateAt's doc comment.
func TestTruncateAt_KeepsStartVClock(t *testing.T) {
	dir := t.TempDir()

	src := writeChainFile(t, dir, format.VClock{1: 0}, nil, [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2)},
		{row(t, 1, 3)},
		{row(t, 1, 4)},
		{row(t, 1, 5)},
	})

	// Truncate under the very name the source carries, so the result is
	// indexable exactly where Tarantool would look for it.
	outDir := t.TempDir()
	dst := filepath.Join(outDir, filepath.Base(src))
	require.NoError(t, TruncateAt(src, dst, 1, 3))

	require.Equal(t, format.VClock{1: 0}, readMeta(t, dst).VClock)

	_, err := xdir.OpenDir(outDir, format.FiletypeXLOG)
	require.NoError(t, err, "a truncated file must still index under its own name")
}

// PrevVClock points at the previous file in the chain and is likewise
// untouched by truncation.
func TestTruncateAt_KeepsPrevVClock(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src,
		xlogMeta(t, testUUID, format.VClock{1: 10}, format.VClock{1: 4}),
		[][]format.XRow{{row(t, 1, 11)}, {row(t, 1, 12)}})

	require.NoError(t, TruncateAt(src, dst, 1, 11))

	meta := readMeta(t, dst)
	require.Equal(t, format.VClock{1: 10}, meta.VClock)
	require.Equal(t, format.VClock{1: 4}, meta.PrevVClock)
}

// A multi-row tx straddling the point is kept whole — truncation never splits
// a transaction.
func TestTruncateAt_MultiRowTxAcrossPointKeptWhole(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
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

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
		{row(t, 1, 1), row(t, 1, 2)},
		{row(t, 1, 3), row(t, 1, 4)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 2))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}}, readAllRows(t, dst))
}

// Truncating on replica 1 keeps replica-2 rows riding along in kept txs; the
// start vclock covers both replicas and neither axis is rewritten.
func TestTruncateAt_MultiReplica(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0, 2: 0}, nil), [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 2, 1), row(t, 1, 2)},
		{row(t, 2, 2), row(t, 1, 3)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 2))

	require.Equal(t, []rowKey{{1, 1}, {2, 1}, {1, 2}}, readAllRows(t, dst))
	require.Equal(t, format.VClock{1: 0, 2: 0}, readMeta(t, dst).VClock)
}

// Point on the last row: output equals input.
func TestTruncateAt_PointOnLastRow(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
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

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 0}, nil), [][]format.XRow{
		{row(t, 1, 1)}, {row(t, 1, 2)}, {row(t, 1, 3)}, {row(t, 1, 4)},
	})

	require.NoError(t, TruncateAt(src, dst, 1, 2))

	require.Equal(t, []rowKey{{1, 1}, {1, 2}}, readAllRows(t, dst))
}
