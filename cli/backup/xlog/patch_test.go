package xlog

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-xlog/format"
)

const patchedUUID = "22222222-2222-2222-2222-222222222222"

// metaEnd returns the offset just past the header's blank-line terminator,
// where tx blocks begin.
func metaEnd(b []byte) int {
	i := bytes.Index(b, []byte("\n\n"))
	if i < 0 {
		return len(b)
	}

	return i + 2
}

func TestPatchInstanceUUID_OnCopy_XLOG(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	meta := xlogMeta(t, testUUID, format.VClock{1: 3}, format.VClock{1: 0})
	writeXlog(t, src, meta, [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2)},
		{row(t, 1, 3)},
	})

	require.NoError(t, PatchInstanceUUID(src, dst, patchedUUID))

	dm := readMeta(t, dst)
	require.Equal(t, patchedUUID, dm.InstanceUUID.String())

	// Everything else in the header is preserved.
	sm := readMeta(t, src)
	require.Equal(t, sm.Version, dm.Version)
	require.Equal(t, sm.FormatVer, dm.FormatVer)
	require.Equal(t, sm.VClock, dm.VClock)
	require.Equal(t, sm.PrevVClock, dm.PrevVClock)

	// Source untouched, rows identical.
	require.Equal(t, testUUID, readMeta(t, src).InstanceUUID.String())
	require.Equal(t, readAllRows(t, src), readAllRows(t, dst))
}

// Bodies and CRCs are preserved byte-for-byte: the tail after the header is
// identical between src and dst.
func TestPatchInstanceUUID_PreservesTailBytes(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	meta := xlogMeta(t, testUUID, format.VClock{1: 2}, nil)
	writeXlog(t, src, meta, [][]format.XRow{
		{row(t, 1, 1), row(t, 1, 2)},
	})

	require.NoError(t, PatchInstanceUUID(src, dst, patchedUUID))

	sb := fileBytes(t, src)
	db := fileBytes(t, dst)

	require.Equal(t, sb[metaEnd(sb):], db[metaEnd(db):])
}

func TestPatchInstanceUUID_OnCopy_SNAP(t *testing.T) {
	src := tmpPath(t, "src.snap")
	dst := tmpPath(t, "dst.snap")

	meta := snapMeta(t, testUUID, format.VClock{1: 2})
	writeXlog(t, src, meta, [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2)},
	})

	require.NoError(t, PatchInstanceUUID(src, dst, patchedUUID))

	dm := readMeta(t, dst)
	require.Equal(t, format.FiletypeSNAP, dm.Filetype)
	require.Equal(t, patchedUUID, dm.InstanceUUID.String())
	require.Equal(t, readAllRows(t, src), readAllRows(t, dst))
}

// src == dst re-stamps in place, leaving the rest of the file intact.
func TestPatchInstanceUUID_InPlace(t *testing.T) {
	path := tmpPath(t, "inplace.xlog")

	meta := xlogMeta(t, testUUID, format.VClock{1: 2}, nil)
	writeXlog(t, path, meta, [][]format.XRow{
		{row(t, 1, 1)},
		{row(t, 1, 2)},
	})

	before := fileBytes(t, path)

	require.NoError(t, PatchInstanceUUID(path, path, patchedUUID))

	require.Equal(t, patchedUUID, readMeta(t, path).InstanceUUID.String())

	after := fileBytes(t, path)
	require.Equal(t, before[metaEnd(before):], after[metaEnd(after):])
	require.Len(t, after, len(before))
}

func TestPatchInstanceUUID_InvalidUUID(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 1}, nil),
		[][]format.XRow{{row(t, 1, 1)}})

	err := PatchInstanceUUID(src, dst, "not-a-uuid")
	require.ErrorIs(t, err, ErrInvalidUUID)
}

// A no-op re-stamp (new == current) still succeeds.
func TestPatchInstanceUUID_SameUUID(t *testing.T) {
	src := tmpPath(t, "src.xlog")
	dst := tmpPath(t, "dst.xlog")

	writeXlog(t, src, xlogMeta(t, testUUID, format.VClock{1: 1}, nil),
		[][]format.XRow{{row(t, 1, 1)}})

	require.NoError(t, PatchInstanceUUID(src, dst, testUUID))
	require.Equal(t, testUUID, readMeta(t, dst).InstanceUUID.String())
}
