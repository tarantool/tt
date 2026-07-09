package xlog

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-xlog/format"
)

// buildChain builds three .xlog files on replica 1:
//
//	A: vclock {1:0}  rows 1..3  -> 0.xlog
//	B: vclock {1:3}  rows 4..6  -> 3.xlog
//	C: vclock {1:6}  rows 7..9  -> 6.xlog
//
// Each file's VClock is its start boundary; it holds rows with lsn strictly
// greater than that value (Tarantool's <signature>.xlog convention).
// dir.LocateLSN returns the file with the largest VClock[replica] <= lsn.
func buildChain(t *testing.T, dir string) (a, b, c string) {
	t.Helper()

	a = writeChainFile(t, dir, format.VClock{1: 0}, nil, [][]format.XRow{
		{row(t, 1, 1)}, {row(t, 1, 2)}, {row(t, 1, 3)},
	})
	b = writeChainFile(t, dir, format.VClock{1: 3}, format.VClock{1: 0}, [][]format.XRow{
		{row(t, 1, 4)}, {row(t, 1, 5)}, {row(t, 1, 6)},
	})
	c = writeChainFile(t, dir, format.VClock{1: 6}, format.VClock{1: 3}, [][]format.XRow{
		{row(t, 1, 7)}, {row(t, 1, 8)}, {row(t, 1, 9)},
	})

	return a, b, c
}

func TestFindTrimFile_MidChain(t *testing.T) {
	dir := t.TempDir()
	a, b, _ := buildChain(t, dir)

	got, err := FindTrimFile(dir, 1, 2) // lsn 2 in A (start 0 <= 2)
	require.NoError(t, err)
	require.Equal(t, a, got)

	got, err = FindTrimFile(dir, 1, 5) // lsn 5 in B (start 3 <= 5)
	require.NoError(t, err)
	require.Equal(t, b, got)
}

func TestFindTrimFile_LastFile(t *testing.T) {
	dir := t.TempDir()
	_, _, c := buildChain(t, dir)

	got, err := FindTrimFile(dir, 1, 8)
	require.NoError(t, err)
	require.Equal(t, c, got)
}

// lsn equal to a file's start vclock resolves to that file (largest VClock <= lsn).
func TestFindTrimFile_OnBoundary(t *testing.T) {
	dir := t.TempDir()
	_, b, _ := buildChain(t, dir)

	got, err := FindTrimFile(dir, 1, 3)
	require.NoError(t, err)
	require.Equal(t, b, got)
}

func TestFindTrimFile_AboveChain(t *testing.T) {
	dir := t.TempDir()
	_, _, c := buildChain(t, dir)

	got, err := FindTrimFile(dir, 1, 100)
	require.NoError(t, err)
	require.Equal(t, c, got)
}

func TestFindTrimFile_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	_, err := FindTrimFile(dir, 1, 1)
	require.ErrorIs(t, err, ErrTrimFileNotFound)
}

func TestFindTrimFile_BelowChain(t *testing.T) {
	dir := t.TempDir()
	writeChainFile(t, dir, format.VClock{1: 10}, nil, [][]format.XRow{{row(t, 1, 11)}})

	_, err := FindTrimFile(dir, 1, -1)
	require.ErrorIs(t, err, ErrTrimFileNotFound)
}

// For a replica absent from every file all VClock[replica]=0 entries tie;
// LocateLSN breaks ties toward the last (highest-signature) file.
func TestFindTrimFile_UnknownReplicaTiesToLast(t *testing.T) {
	dir := t.TempDir()
	_, _, c := buildChain(t, dir)

	got, err := FindTrimFile(dir, 2, 0)
	require.NoError(t, err)
	require.Equal(t, c, got)
}
