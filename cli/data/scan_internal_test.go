package data

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"
)

// multiReplicaXlog writes an xlog of a master of a replicaset of two: its own
// rows (replica 1), rows it received from replica 2 and a row of a local
// space (replica 0).
func multiReplicaXlog(t *testing.T, dir string) string {
	t.Helper()

	return writeJournal(t, dir, format.FiletypeXLOG, format.VClock{}, nil,
		tx(insert(t, 1, 1, 512, 1)),                          // A.
		tx(insert(t, 2, 1, 512, 100)),                        // B.
		tx(insert(t, 1, 2, 512, 2), insert(t, 1, 3, 512, 3)), // C.
		tx(insert(t, 2, 2, 512, 101)),                        // D.
		tx(insert(t, 1, 4, 512, 4)),                          // E.
		tx(insert(t, 0, 1, 513, 5)),                          // F, a local space.
	)
}

func TestScanVClockBounds(t *testing.T) {
	t.Parallel()

	path := multiReplicaXlog(t, t.TempDir())

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{
			name:   "none",
			filter: Filter{},
			want:   []string{"1:1", "2:1", "1:2,1:3", "2:2", "1:4", "0:1"},
		},
		{
			// From is exclusive, to inclusive; a replica the vclock does
			// not name has LSN 0, so the local row is past --to-vclock.
			name: "both",
			filter: Filter{
				FromVClock: format.VClock{1: 1, 2: 1},
				ToVClock:   format.VClock{1: 3, 2: 2},
			},
			want: []string{"1:2,1:3", "2:2"},
		},
		{
			// --to-vclock 1:2 falls inside the transaction of 1:2 and
			// 1:3, which is taken whole.
			name:   "inside a transaction",
			filter: Filter{ToVClock: format.VClock{0: 1, 1: 2, 2: 2}},
			want:   []string{"1:1", "2:1", "1:2,1:3", "2:2", "0:1"},
		},
		{
			name:   "from inside a transaction",
			filter: Filter{FromVClock: format.VClock{1: 2, 2: 2, 0: 1}},
			want:   []string{"1:2,1:3", "1:4"},
		},
		{
			name:   "replica",
			filter: Filter{Replicas: []uint32{2}},
			want:   []string{"2:1", "2:2"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			filter := testCase.filter
			// The local space is not a system one, and no row is.
			assert.Equal(t, testCase.want, lsns(scan(t, plan(t, &filter, path), &filter)))
		})
	}
}

func TestScanLSNBounds(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	single := writeJournal(t, dir, format.FiletypeXLOG, format.VClock{}, nil,
		tx(insert(t, 1, 1, 512, 1)), tx(insert(t, 1, 2, 512, 2)),
		tx(insert(t, 1, 3, 512, 3), insert(t, 1, 4, 512, 4)), tx(insert(t, 1, 5, 512, 5)),
		tx(insert(t, 1, 6, 512, 6)))

	// Both ends are included.
	filter := Filter{From: 2, To: 3, HasFrom: true, HasTo: true}
	assert.Equal(t, []string{"1:2", "1:3,1:4"}, lsns(scan(t, plan(t, &filter, single), &filter)))

	filter = Filter{From: 4, To: 5, HasFrom: true, HasTo: true}
	assert.Equal(t, []string{"1:3,1:4", "1:5"}, lsns(scan(t, plan(t, &filter, single), &filter)))

	filter = Filter{To: 1, HasTo: true}
	assert.Equal(t, []string{"1:1"}, lsns(scan(t, plan(t, &filter, single), &filter)))

	// With several replicas, --replica names the one whose LSN is bound;
	// the transactions it selects are whole, and those with no row of it
	// are not selected.
	multi := multiReplicaXlog(t, t.TempDir())

	filter = Filter{From: 2, To: 2, HasFrom: true, HasTo: true, Replicas: []uint32{1}}
	assert.Equal(t, []string{"1:2,1:3"}, lsns(scan(t, plan(t, &filter, multi), &filter)))

	// Without it, the rows of a second replica are an error.
	filter = Filter{From: 2, HasFrom: true}

	err := Scan(plan(t, &filter, multi), &filter, ScanOptions{}, func(*Txn) error { return nil })
	require.ErrorIs(t, err, ErrAmbiguousReplica)

	// So are headers that name two replicas.
	second := writeJournal(t, t.TempDir(), format.FiletypeXLOG, format.VClock{1: 4, 2: 2},
		format.VClock{}, tx(insert(t, 1, 5, 512, 5)))
	files, err := Collect([]string{second}, CollectOptions{})
	require.NoError(t, err)

	_, err = Select(files, &Filter{To: 5, HasTo: true})
	require.ErrorIs(t, err, ErrAmbiguousReplica)

	err = (&Filter{From: 5, To: 4, HasFrom: true, HasTo: true}).Check()
	require.ErrorIs(t, err, ErrEmptyRange)

	err = (&Filter{From: 5, HasFrom: true, ToVClock: format.VClock{}}).Check()
	require.ErrorIs(t, err, ErrBoundsConflict)
}

// snapshotChain writes the journals of an instance: an old snapshot at LSN 1,
// a write-ahead log from the start, a snapshot at LSN 3 and a write-ahead log
// after LSN 5.
func snapshotChain(t *testing.T, dir string) {
	t.Helper()

	snapRows := func(count int) [][]format.XRow {
		txs := [][]format.XRow{
			tx(row(iproto.IPROTO_INSERT, 0, 1, 2000, dml(t, 280, 512, 1, "test", "memtx", 0,
				map[string]any{}, []any{}))),
			tx(row(iproto.IPROTO_RAFT, 0, 2, 2000, body(t, uint64(0), 3, uint64(1), 1,
				uint64(2), 3))),
		}

		for i := 1; i <= count; i++ {
			txs = append(txs, tx(row(iproto.IPROTO_INSERT, 0, int64(2+i), 2000,
				dml(t, 512, i))))
		}

		return txs
	}

	writeJournal(t, dir, format.FiletypeSNAP, format.VClock{1: 1}, nil, snapRows(1)...)
	writeJournal(t, dir, format.FiletypeXLOG, format.VClock{}, nil,
		tx(insert(t, 1, 1, 512, 1)), tx(insert(t, 1, 2, 512, 2)), tx(insert(t, 1, 3, 512, 3)),
		tx(insert(t, 1, 4, 512, 4)), tx(insert(t, 1, 5, 512, 5)))
	writeJournal(t, dir, format.FiletypeSNAP, format.VClock{1: 3}, nil, snapRows(3)...)
	writeJournal(t, dir, format.FiletypeXLOG, format.VClock{1: 5}, format.VClock{},
		tx(insert(t, 1, 6, 512, 6)), tx(insert(t, 1, 7, 512, 7)))
}

func TestScanSnapshotChain(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	snapshotChain(t, dir)

	// The newest snapshot, then the rows of the logs it does not contain.
	filter := Filter{}
	got := scan(t, plan(t, &filter, dir), &filter)
	assert.Equal(t, []string{"0:3", "0:4", "0:5", "1:4", "1:5", "1:6", "1:7"}, lsns(got))
	assert.Equal(t, "00000000000000000003.snap", got[0].file)
	assert.Equal(t, "00000000000000000000.xlog", got[3].file)

	// A snapshot past --to is left out for an older one.
	filter = Filter{To: 2, HasTo: true}
	got = scan(t, plan(t, &filter, dir), &filter)
	assert.Equal(t, []string{"0:3", "1:2"}, lsns(got))
	assert.Equal(t, "00000000000000000001.snap", got[0].file)

	filter = Filter{ToVClock: format.VClock{1: 4}}
	assert.Equal(t, []string{"0:3", "0:4", "0:5", "1:4"},
		lsns(scan(t, plan(t, &filter, dir), &filter)))

	// A lower bound reads no snapshot.
	filter = Filter{From: 5, HasFrom: true}
	assert.Equal(t, []string{"1:5", "1:6", "1:7"}, lsns(scan(t, plan(t, &filter, dir), &filter)))

	filter = Filter{}

	chain := plan(t, &filter, dir)
	require.Len(t, chain.Files, 3)
	assert.Equal(t, chain.Base, chain.Files[0])

	// A log a snapshot contains whole is not even read.
	covered := t.TempDir()
	writeJournal(t, covered, format.FiletypeXLOG, format.VClock{}, nil,
		tx(insert(t, 1, 1, 512, 1)), tx(insert(t, 1, 2, 512, 2)))
	writeJournal(t, covered, format.FiletypeSNAP, format.VClock{1: 2}, nil,
		tx(row(iproto.IPROTO_INSERT, 0, 1, 2000, dml(t, 512, 1))))
	writeJournal(t, covered, format.FiletypeXLOG, format.VClock{1: 2}, format.VClock{},
		tx(insert(t, 1, 3, 512, 3)))

	coveredPlan := plan(t, &filter, covered)
	require.Len(t, coveredPlan.Files, 2)
	assert.Equal(t, "00000000000000000002.xlog", filepath.Base(coveredPlan.Files[1].Path))
	assert.Equal(t, []string{"0:1", "1:3"}, lsns(scan(t, coveredPlan, &filter)))

	ids, err := SpaceIDs(chain)
	require.NoError(t, err)
	assert.Equal(t, map[string]uint32{"test": 512}, ids)
}

func TestScanSystemRows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	snapshotChain(t, dir)

	// Without --show-system neither the _space row nor the RAFT row of the
	// snapshot is shown.
	filter := Filter{}
	for _, txn := range scan(t, plan(t, &filter, dir), &filter) {
		assert.NotEqual(t, "0:1", txn.rows[0])
		assert.NotEqual(t, "0:2", txn.rows[0])
	}

	filter = Filter{ShowSystem: true}
	assert.Equal(t, []string{"0:1", "0:2", "0:3", "0:4", "0:5", "1:4", "1:5", "1:6", "1:7"},
		lsns(scan(t, plan(t, &filter, dir), &filter)))

	// --space selects system spaces too, and no other row.
	filter = Filter{Spaces: []uint32{280}}
	assert.Equal(t, []string{"0:1"}, lsns(scan(t, plan(t, &filter, dir), &filter)))
}

func TestScanTimestamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	snapshotChain(t, dir)

	// The rows of the logs are at 1000 + LSN, the snapshots at 2000.
	filter := Filter{Before: 1004.5}
	assert.Equal(t, []string{"1:1", "1:2", "1:3", "1:4"},
		lsns(scan(t, plan(t, &filter, dir), &filter)))

	filter = Filter{Before: 2001}
	assert.Equal(t, []string{"0:3", "0:4", "0:5", "1:4", "1:5", "1:6", "1:7"},
		lsns(scan(t, plan(t, &filter, dir), &filter)))
}

func TestCollect(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	snapshotChain(t, dir)

	_, err := Collect([]string{dir + "/missing"}, CollectOptions{})
	require.Error(t, err)

	_, err = Collect([]string{t.TempDir()}, CollectOptions{})
	require.ErrorIs(t, err, ErrNoJournals)

	files, err := Collect([]string{dir, dir + "/00000000000000000005.xlog"}, CollectOptions{})
	require.NoError(t, err)

	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Path[len(dir)+1:])
	}

	assert.Equal(t, []string{
		"00000000000000000000.xlog", "00000000000000000001.snap",
		"00000000000000000003.snap", "00000000000000000005.xlog",
	}, names)
}
