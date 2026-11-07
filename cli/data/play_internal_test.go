package data

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/go-xlog/format"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/tt/v3/cli/connector"
)

// targetScript is an instance with the space the fixtures change, 512, of
// memtx, and a vinyl space, 514. It listens once they are made.
const targetScript = `box.cfg{log_level = 1,
    memtx_use_mvcc_engine = os.getenv('MVCC') == '1'}
box.schema.space.create('test', {id = 512}):create_index('pk')
box.schema.space.create('vinyl', {id = 514, engine = 'vinyl'}):create_index('pk')
box.schema.user.grant('guest', 'super')
box.cfg{listen = os.getenv('LISTEN')}
`

// startTarget starts a Tarantool instance with the target script and
// returns a connection to it; it skips the test without Tarantool.
func startTarget(t *testing.T, mvcc bool) *tarantool.Connection {
	t.Helper()

	binary, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found in PATH")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "target.lua")
	socket := filepath.Join(shortTempDir(t), "target.sock")

	require.NoError(t, os.WriteFile(script, []byte(targetScript), 0o600))

	cmd := exec.CommandContext(t.Context(), binary, script)

	cmd.Dir = dir

	cmd.Env = append(os.Environ(), "LISTEN="+socket, fmt.Sprintf("MVCC=%d", map[bool]int{
		false: 0, true: 1,
	}[mvcc]))
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var conn *tarantool.Connection

	require.Eventually(t, func() bool {
		conn, err = connector.ConnectIproto(t.Context(), connector.ConnectOpts{
			Network: connector.UnixNetwork, Address: socket,
		}, TargetOpts())

		return err == nil
	}, 10*time.Second, 50*time.Millisecond)

	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// shortTempDir returns a directory under /tmp, whose path leaves room for a
// unix socket in it.
func shortTempDir(t *testing.T) string {
	t.Helper()

	//nolint:usetesting // t.TempDir is too deep for a socket path on macOS.
	dir, err := os.MkdirTemp("/tmp", "ttd")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

// tuples returns the tuples of a space of the target.
func tuples(t *testing.T, conn *tarantool.Connection, space uint32) []any {
	t.Helper()

	data, err := conn.Do(tarantool.NewSelectRequest(space).Index(uint32(0)).
		Iterator(tarantool.IterAll).
		Limit(1000)).Get()
	require.NoError(t, err)

	return data
}

// playXlog writes the transactions of a play: two of one row, one of two
// rows, one of an update with index base 1 and one of the vinyl space.
func playXlog(t *testing.T, dir string) string {
	t.Helper()

	return writeJournal(t, dir, format.FiletypeXLOG, format.VClock{}, nil,
		tx(insert(t, 1, 1, 512, 1, "a")),
		tx(insert(t, 1, 2, 512, 2, "b")),
		tx(insert(t, 1, 3, 512, 3, "c"), insert(t, 1, 4, 512, 4, "d")),
		tx(row(iproto.IPROTO_UPDATE, 1, 5, 1005, body(t, uint64(iproto.IPROTO_SPACE_ID), 512,
			uint64(iproto.IPROTO_INDEX_BASE), 1, uint64(iproto.IPROTO_KEY), []any{1},
			uint64(iproto.IPROTO_TUPLE), []any{[]any{"=", 2, "x"}}))),
		tx(insert(t, 1, 6, 514, 6, "v")),
	)
}

func play(t *testing.T, conn *tarantool.Connection, path string, opts PlayOptions) (
	*Summary, error,
) {
	t.Helper()

	target, err := NewTarget(conn)
	require.NoError(t, err)

	if opts.Batch == 0 {
		opts.Batch = DefaultBatch
	}

	filter := Filter{}

	return Play(t.Context(), target, plan(t, &filter, path), &filter, opts)
}

func TestPlayInStreamTransactions(t *testing.T) {
	t.Parallel()

	conn := startTarget(t, true)
	path := playXlog(t, t.TempDir())

	summary, err := play(t, conn, path, PlayOptions{TxnMode: TxnStream, OnError: OnErrorStop})
	require.NoError(t, err)
	assert.Equal(t, 5, summary.TxnsApplied)
	assert.Equal(t, 6, summary.RowsApplied)
	assert.False(t, summary.RowByRow)

	assert.Equal(t, []any{
		[]any{int8(1), "x"}, []any{int8(2), "b"}, []any{int8(3), "c"}, []any{int8(4), "d"},
	}, tuples(t, conn, 512))
	assert.Len(t, tuples(t, conn, 514), 1)

	// A second play refuses every insert. A transaction of two rows is
	// rolled back whole, and --on-error skip goes on past it.
	summary, err = play(t, conn, path, PlayOptions{TxnMode: TxnStream, OnError: OnErrorSkip})
	require.NoError(t, err)
	assert.Equal(t, 1, summary.TxnsApplied, "the update")
	assert.Equal(t, 4, summary.TxnsFailed)
	assert.Equal(t, 4, summary.RowsFailed)
	assert.Equal(t, 1, summary.RowsSkipped)
	require.Len(t, summary.Failures, 4)
	assert.Equal(t, int64(3), summary.Failures[2].LSN)
	assert.Contains(t, summary.Failures[0].Error(),
		"the INSERT of space 512 at LSN 1 of replica 1 of "+path+": Duplicate key exists")
}

func TestPlayRollsBackARefusedTransaction(t *testing.T) {
	t.Parallel()

	conn := startTarget(t, true)

	_, err := conn.Do(tarantool.NewInsertRequest(uint32(512)).Tuple([]any{4, "old"})).Get()
	require.NoError(t, err)

	path := playXlog(t, t.TempDir())

	// The transaction of 3 and 4 is refused for 4: 3 is not applied either,
	// and the play stops there.
	summary, err := play(t, conn, path, PlayOptions{TxnMode: TxnAuto, OnError: OnErrorStop})
	require.ErrorIs(t, err, ErrRefused)
	assert.Equal(t, 2, summary.TxnsApplied)
	assert.Equal(t, 1, summary.TxnsFailed)
	assert.Equal(t, 1, summary.RowsSkipped)
	assert.Equal(t, []any{
		[]any{int8(1), "a"}, []any{int8(2), "b"}, []any{int8(4), "old"},
	}, tuples(t, conn, 512))
}

func TestPlayWithoutMVCC(t *testing.T) {
	t.Parallel()

	conn := startTarget(t, false)
	path := playXlog(t, t.TempDir())

	// Stream mode cannot apply the transaction of two rows to memtx.
	summary, err := play(t, conn, path, PlayOptions{TxnMode: TxnStream, OnError: OnErrorStop})
	require.ErrorIs(t, err, ErrRefused)
	require.ErrorContains(t, err, "memtx_use_mvcc_engine")
	assert.Equal(t, 2, summary.TxnsApplied)
	assert.Equal(t, 2, summary.RowsSkipped)
	assert.Len(t, tuples(t, conn, 512), 2)

	// Auto mode goes on row by row from it.
	_, err = conn.Do(tarantool.NewCallRequest("box.space.test:truncate")).Get()
	require.NoError(t, err)

	summary, err = play(t, conn, path, PlayOptions{TxnMode: TxnAuto, OnError: OnErrorStop})
	require.NoError(t, err)
	assert.True(t, summary.RowByRow)
	assert.Equal(t, 5, summary.TxnsApplied)
	assert.Len(t, tuples(t, conn, 512), 4)
}

func TestPlayRowByRow(t *testing.T) {
	t.Parallel()

	conn := startTarget(t, false)
	path := playXlog(t, t.TempDir())

	summary, err := play(t, conn, path, PlayOptions{TxnMode: TxnRow, OnError: OnErrorStop,
		Batch: 1})
	require.NoError(t, err)
	assert.Equal(t, 6, summary.RowsApplied)

	// Row by row, a refused row leaves the others of its transaction.
	summary, err = play(t, conn, path, PlayOptions{TxnMode: TxnRow, OnError: OnErrorSkip})
	require.NoError(t, err)
	assert.Equal(t, 4, summary.TxnsFailed)
	assert.Equal(t, 5, summary.RowsFailed)
	assert.Equal(t, 0, summary.RowsSkipped)
}

// TestPlayKeepsTheOrderOfTheRequestsInFlight plays a key inserted and
// deleted over and over, with a batch of requests in flight: a request sent
// before the one ahead of it would insert the key twice or leave it behind.
func TestPlayKeepsTheOrderOfTheRequestsInFlight(t *testing.T) {
	t.Parallel()

	conn := startTarget(t, false)

	const times = 400

	txs := make([][]format.XRow, 0, 2*times)
	for index := range times {
		lsn := int64(2*index + 1)

		txs = append(txs, tx(insert(t, 1, lsn, 512, 1, index)),
			tx(row(iproto.IPROTO_DELETE, 1, lsn+1, 1000, body(t,
				uint64(iproto.IPROTO_SPACE_ID), 512, uint64(iproto.IPROTO_KEY), []any{1}))))
	}

	path := writeJournal(t, t.TempDir(), format.FiletypeXLOG, format.VClock{}, nil, txs...)

	summary, err := play(t, conn, path, PlayOptions{TxnMode: TxnAuto, OnError: OnErrorStop})
	require.NoError(t, err)
	assert.Equal(t, 2*times, summary.RowsApplied)
	assert.Empty(t, tuples(t, conn, 512))
}

func TestPlayDryRun(t *testing.T) {
	t.Parallel()

	conn := startTarget(t, true)
	dir := t.TempDir()
	path := writeJournal(t, dir, format.FiletypeXLOG, format.VClock{}, nil,
		tx(insert(t, 1, 1, 512, 1)), tx(insert(t, 1, 2, 513, 1)), tx(insert(t, 1, 3, 513, 2)))

	target, err := NewTarget(conn)
	require.NoError(t, err)

	spaces, err := target.Spaces()
	require.NoError(t, err)
	assert.Equal(t, "test", spaces[512])

	summary, err := play(t, conn, path, PlayOptions{DryRun: true, Spaces: spaces})
	require.ErrorIs(t, err, ErrMissingSpaces)
	assert.Equal(t, map[uint32]int{513: 2}, summary.Missing)
	assert.Equal(t, 3, summary.Rows)
	assert.Empty(t, tuples(t, conn, 512))
}

// TestRowRequestKeepsIndexBaseAndArrow pins the keys a request takes from
// its row: the index a DELETE or UPDATE looks its key up in, the base its
// field numbers count from, which the target rebases, and a batch of Arrow.
func TestRowRequestKeepsIndexBaseAndArrow(t *testing.T) {
	t.Parallel()

	decode := func(request rowRequest) map[uint64]any {
		var got map[uint64]any

		require.NoError(t, msgpack.Unmarshal(request.body, &got))

		return got
	}

	update := &Row{Type: iproto.IPROTO_UPDATE, DML: &format.DMLBody{
		SpaceID: 512, IndexID: 1, IndexBase: 1,
		Key: []byte{0x91, 0xa1, 'b'}, Tuple: []byte{0x91, 0x93, 0xa1, '=', 0x02, 0xa1, 'x'},
	}}
	got := decode(newRowRequest(t.Context(), update))
	assert.EqualValues(t, 512, got[uint64(iproto.IPROTO_SPACE_ID)])
	assert.EqualValues(t, 1, got[uint64(iproto.IPROTO_INDEX_ID)])
	assert.EqualValues(t, 1, got[uint64(iproto.IPROTO_INDEX_BASE)])
	assert.Equal(t, []any{"b"}, got[uint64(iproto.IPROTO_KEY)])

	// A row of vinyl counts its fields from 0 and carries no base.
	upsert := &Row{Type: iproto.IPROTO_UPSERT, DML: &format.DMLBody{
		SpaceID: 513, Tuple: []byte{0x91, 0x01}, Ops: []byte{0x90},
	}}

	got = decode(newRowRequest(t.Context(), upsert))
	assert.NotContains(t, got, uint64(iproto.IPROTO_INDEX_BASE))
	assert.NotContains(t, got, uint64(iproto.IPROTO_INDEX_ID))

	arrow := &Row{Type: iproto.IPROTO_INSERT_ARROW, DML: &format.DMLBody{
		SpaceID: 512, Arrow: []byte{0xc7, 0x03, 0x08, 'A', 'B', 'C'},
	}}
	request := newRowRequest(t.Context(), arrow)
	assert.Equal(t, iproto.IPROTO_INSERT_ARROW, request.Type())

	var raw map[uint64]msgpack.RawMessage

	require.NoError(t, msgpack.Unmarshal(request.body, &raw))
	assert.Len(t, raw, 2)
	assert.Equal(t, msgpack.RawMessage(arrow.DML.Arrow), raw[uint64(iproto.IPROTO_ARROW)])
}
