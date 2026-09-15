package xlog

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-xlog/format"
)

// writeSnapFile writes one .snap named after its vclock signature, the
// <signature>.snap convention dir.OpenDir validates, and returns the path.
func writeSnapFile(t *testing.T, dir string, vclock format.VClock) string {
	t.Helper()

	path := filepath.Join(dir, fmt.Sprintf("%020d.snap", vclock.Signature()))
	writeXlog(t, path, snapMeta(t, testUUID, vclock), nil)

	return path
}

// Snapshots and journals live in directories of their own, and each directory
// is read for the kind of file it is meant to hold. A snapshot found in the
// journal directory is not part of the chain the trim reasons about -- it is
// somebody else's file, and removing it would take a file the caller never
// named.
func TestJournalsAfter_ReadsEachDirForItsOwnKind(t *testing.T) {
	snapshotDir := t.TempDir()
	walDir := t.TempDir()

	keptSnap := writeSnapFile(t, snapshotDir, format.VClock{1: 0})
	lateSnap := writeSnapFile(t, snapshotDir, format.VClock{1: 10})

	keptXlog := writeChainFile(t, walDir, format.VClock{1: 5}, format.VClock{1: 0}, nil)
	lateXlog := writeChainFile(t, walDir, format.VClock{1: 10}, format.VClock{1: 5}, nil)

	strandedSnap := writeSnapFile(t, walDir, format.VClock{1: 12})

	after, err := JournalsAfter(snapshotDir, walDir, 5)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{lateSnap, lateXlog}, after)

	for _, path := range []string{keptSnap, keptXlog, strandedSnap} {
		assert.NotContains(t, after, path)
	}
}

// The point can sit at the very end of what the backup carries, and then
// nothing reaches past it.
func TestJournalsAfter_NothingPastTheEnd(t *testing.T) {
	snapshotDir := t.TempDir()
	walDir := t.TempDir()

	writeSnapFile(t, snapshotDir, format.VClock{1: 0})
	writeChainFile(t, walDir, format.VClock{1: 5}, format.VClock{1: 0}, nil)

	after, err := JournalsAfter(snapshotDir, walDir, 5)
	require.NoError(t, err)

	assert.Empty(t, after)
}

// One directory named twice is how an instance that configures none of the
// keys is described, and both kinds have to be found in it.
func TestJournalsAfter_OneDirectoryTwice(t *testing.T) {
	dir := t.TempDir()

	writeSnapFile(t, dir, format.VClock{1: 0})
	lateSnap := writeSnapFile(t, dir, format.VClock{1: 10})
	writeChainFile(t, dir, format.VClock{1: 5}, format.VClock{1: 0}, nil)
	lateXlog := writeChainFile(t, dir, format.VClock{1: 10}, format.VClock{1: 5}, nil)

	after, err := JournalsAfter(dir, dir, 5)
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{lateSnap, lateXlog}, after)
}
