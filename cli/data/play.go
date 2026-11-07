package data

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/go-xlog/format"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/connector"
)

// TxnMode is how Play applies a transaction of several rows.
type TxnMode string

const (
	// TxnAuto applies a transaction of several rows in an IPROTO stream
	// transaction while the target takes them, and row by row from the first
	// one it cannot take on: one of a memtx space with
	// memtx_use_mvcc_engine off, which a transaction that spans requests
	// cannot change, or a target without IPROTO transactions.
	TxnAuto TxnMode = "auto"
	// TxnStream applies a transaction of several rows in an IPROTO stream
	// transaction, and fails on a target that cannot take it.
	TxnStream TxnMode = "stream"
	// TxnRow applies every row on its own.
	TxnRow TxnMode = "row"
)

// TxnModes are the transaction modes, in the order the help lists them.
var TxnModes = []TxnMode{TxnAuto, TxnStream, TxnRow}

// OnError is what Play does when the target refuses a transaction.
type OnError string

const (
	// OnErrorStop stops at the first transaction the target refuses.
	OnErrorStop OnError = "stop"
	// OnErrorSkip reports a transaction the target refuses and goes on.
	OnErrorSkip OnError = "skip"
)

// OnErrors are the error policies, in the order the help lists them.
var OnErrors = []OnError{OnErrorStop, OnErrorSkip}

// DefaultBatch is how many requests Play has in flight by default.
const DefaultBatch = 128

var (
	// ErrRefused is a transaction the target refused.
	ErrRefused = errors.New("the target refused a transaction")
	// ErrNoTransactions is TxnStream with a target that has no IPROTO
	// transactions.
	ErrNoTransactions = errors.New(
		"the target does not support transactions in IPROTO streams (Tarantool 2.10+)")
	// ErrMissingSpaces is a dry run that found rows of spaces the target
	// does not have.
	ErrMissingSpaces = errors.New("the target has no spaces the rows change")
)

// Target is the instance Play applies transactions to: a connection and,
// when the instance supports them, the IPROTO stream all requests go in.
type Target struct {
	conn *tarantool.Connection
	// doer sends a request: the stream, or the connection without one.
	doer tarantool.Doer
	// transactions reports whether the stream can carry transactions.
	transactions bool
}

// TargetOpts are the options of a connection to a target. A connection
// packs a request into one of Concurrency buffers, by its ID, and writes the
// buffers out one after another: with more than one, a request goes out
// ahead of those sent before it once the IDs in flight wrap around the
// buffers. One buffer writes the requests in the order Play sends them.
func TargetOpts() tarantool.Opts {
	return tarantool.Opts{Concurrency: 1}
}

// NewTarget makes the target of conn, a connection of TargetOpts. Every
// request goes in one IPROTO stream, which the instance processes one request
// after another, in the order they arrive, even when many are in flight; a
// target without streams gets the requests outside of any, which memtx
// applies in the order they arrive but vinyl, which may yield on a read, may
// not.
func NewTarget(conn *tarantool.Connection) (*Target, error) {
	features := conn.ProtocolInfo().Features

	target := &Target{conn: conn, doer: conn, transactions: false}

	if !slices.Contains(features, iproto.IPROTO_FEATURE_STREAMS) {
		log.Warnf("The target does not support IPROTO streams: requests are not ordered " +
			"for vinyl spaces, and transactions of several rows are applied row by row")

		return target, nil
	}

	stream, err := conn.NewStream()
	if err != nil {
		return nil, fmt.Errorf("opening an IPROTO stream: %w", err)
	}

	target.doer = stream
	target.transactions = slices.Contains(features, iproto.IPROTO_FEATURE_TRANSACTIONS)

	return target, nil
}

// Connect connects to the target at opts and makes its Target, which the
// caller closes. name is the target as an error names it. A target the
// connection cannot make a Target of is a failure of the system.
func Connect(ctx context.Context, opts connector.ConnectOpts, name string) (*Target, error) {
	conn, err := connector.ConnectIproto(ctx, opts, TargetOpts())
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", name, err)
	}

	target, err := NewTarget(conn)
	if err != nil {
		_ = conn.Close()

		return nil, sdk.WithCode(sdk.ExitSystem, err)
	}

	return target, nil
}

// Close closes the connection of the target.
func (t *Target) Close() error {
	err := t.conn.Close()
	if err != nil {
		return fmt.Errorf("closing the connection: %w", err)
	}

	return nil
}

// vspaceID is the id of _vspace, the view of the spaces a user may access.
const vspaceID = 281

// Spaces returns the spaces of the target the user may access: their names
// by id.
func (t *Target) Spaces() (map[uint32]string, error) {
	request := tarantool.NewSelectRequest(uint32(vspaceID)).Index(uint32(0)).Limit(math.MaxUint32).
		Iterator(tarantool.IterAll)

	var tuples [][]any

	err := t.conn.Do(request).GetTyped(&tuples)
	if err != nil {
		return nil, fmt.Errorf("reading the spaces of the target: %w", err)
	}

	spaces := make(map[uint32]string, len(tuples))

	for _, fields := range tuples {
		if len(fields) <= spaceFieldName {
			continue
		}

		id, okID := asUint32(fields[spaceFieldID])
		name, okName := fields[spaceFieldName].(string)

		if okID && okName {
			spaces[id] = name
		}
	}

	return spaces, nil
}

// asUint32 returns an integer of any width that fits 32 bits unsigned.
func asUint32(value any) (uint32, bool) {
	reflected := reflect.ValueOf(value)

	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := reflected.Int()

		return uint32(n), n >= 0 && n <= math.MaxUint32 //nolint:gosec // Checked.
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := reflected.Uint()

		return uint32(n), n <= math.MaxUint32 //nolint:gosec // Checked.
	default:
		return 0, false
	}
}

// PlayOptions are how Play reads and applies.
type PlayOptions struct {
	ScanOptions

	// TxnMode is how a transaction of several rows is applied.
	TxnMode TxnMode
	// OnError is what to do when the target refuses a transaction.
	OnError OnError
	// Batch is how many requests are in flight at most; 1 waits for the
	// answer to each before the next.
	Batch int
	// DryRun reads and filters, checks that the target has the spaces the
	// rows change and applies nothing. Spaces are the spaces of the target.
	DryRun bool
	Spaces map[uint32]string
}

// RefusalError is a transaction the target refused: the row it refused and
// why.
type RefusalError struct {
	// File is the file of the transaction.
	File *File
	// Replica and LSN are those of the first row of the transaction.
	Replica uint32
	LSN     int64
	// Row is the row the target refused; nil when it refused the commit.
	Row *Row
	// Err is the error of the target.
	Err error
}

// Error describes the failure.
func (f *RefusalError) Error() string {
	if f.Row == nil {
		return fmt.Sprintf("the transaction at LSN %d of replica %d of %s: %s",
			f.LSN, f.Replica, f.File.Path, f.Err)
	}

	if f.File.Kind == KindSnap {
		// The rows of a snapshot have row numbers, not LSNs.
		return fmt.Sprintf("the %s of space %d at row %d of the snapshot %s: %s",
			format.TypeName(f.Row.Type), f.Row.DML.SpaceID, f.Row.LSN, f.File.Path, f.Err)
	}

	return fmt.Sprintf("the %s of space %d at LSN %d of replica %d of %s: %s",
		format.TypeName(f.Row.Type), f.Row.DML.SpaceID, f.Row.LSN, f.Row.ReplicaID, f.File.Path,
		f.Err)
}

// Unwrap returns the error of the target.
func (f *RefusalError) Unwrap() error {
	return f.Err
}

// Summary is what Play did.
type Summary struct {
	// Txns and Rows are the transactions and the data changes the filter
	// selected and Play went through.
	Txns, Rows int
	// TxnsApplied and RowsApplied are those the target took.
	TxnsApplied, RowsApplied int
	// TxnsFailed are the transactions the target refused a row or the
	// commit of; RowsFailed the rows it refused.
	TxnsFailed, RowsFailed int
	// RowsSkipped are the rows the target did not take for a row of their
	// transaction it refused: the transaction was rolled back.
	RowsSkipped int
	// Failures are the transactions the target refused.
	Failures []*RefusalError
	// Missing are the spaces the target does not have, by id, with the
	// number of rows of each; a dry run finds them.
	Missing map[uint32]int
	// RowByRow reports whether the transactions of several rows were
	// applied row by row, from the first one the target could not take as a
	// whole on in auto mode.
	RowByRow bool
}

// player applies transactions to a target.
type player struct {
	ctx     context.Context //nolint:containedctx // The context of one Play.
	target  *Target
	opts    PlayOptions
	summary Summary
	// pending are the requests in flight, oldest first.
	pending []pendingRow
	// rowByRow is set when transactions are applied row by row.
	rowByRow bool
	// stopped is set by the first refusal under OnErrorStop.
	stopped bool
}

// pendingRow is a row in flight.
type pendingRow struct {
	future tarantool.Future
	txn    *txnState
	row    *Row
}

// txnState is a transaction being applied row by row.
type txnState struct {
	txn     *Txn
	left    int
	applied int
	failed  bool
}

var errStopped = errors.New("stopped")

// Play applies the data changes of the transactions filter selects in plan
// to target, transaction by transaction, in their order, and returns what
// it did. It returns ErrRefused, wrapping the RefusalError, when it stopped at a
// transaction the target refused, and ErrMissingSpaces for a dry run that
// found rows of spaces the target does not have.
//
// The rows that are not data changes - RAFT, synchronous replication, NOP,
// the records of a vinyl log - are never applied.
func Play(ctx context.Context, target *Target, plan *Plan, filter *Filter,
	opts PlayOptions,
) (*Summary, error) {
	if opts.Batch < 1 {
		opts.Batch = 1
	}

	if opts.TxnMode == TxnStream && !target.transactions && !opts.DryRun {
		return nil, ErrNoTransactions
	}

	play := &player{
		ctx: ctx, target: target, opts: opts,
		summary: Summary{
			Txns: 0, Rows: 0, TxnsApplied: 0, RowsApplied: 0, TxnsFailed: 0, RowsFailed: 0,
			RowsSkipped: 0, Failures: nil, Missing: map[uint32]int{}, RowByRow: false,
		},
		pending: nil, rowByRow: opts.TxnMode == TxnRow || !target.transactions,
		stopped: false,
	}

	err := Scan(plan, filter, opts.ScanOptions, play.apply)
	if errors.Is(err, errStopped) {
		err = nil
	}

	drainErr := play.drain()

	play.summary.RowByRow = play.rowByRow

	if err == nil {
		err = drainErr
	}

	if err != nil {
		return &play.summary, err
	}

	if play.stopped {
		return &play.summary, fmt.Errorf("%w: %w", ErrRefused, play.summary.Failures[0])
	}

	if len(play.summary.Missing) > 0 {
		return &play.summary, ErrMissingSpaces
	}

	return &play.summary, nil
}

// apply applies one transaction.
func (p *player) apply(txn *Txn) error {
	if p.stopped {
		return errStopped
	}

	rows := make([]*Row, 0, len(txn.Rows))

	for index := range txn.Rows {
		if txn.Rows[index].DML != nil && txn.File.Kind != KindVylog {
			rows = append(rows, &txn.Rows[index])
		}
	}

	if len(rows) == 0 {
		return nil
	}

	p.summary.Txns++

	p.summary.Rows += len(rows)

	if p.opts.DryRun {
		for _, row := range rows {
			if _, ok := p.opts.Spaces[row.DML.SpaceID]; !ok {
				p.summary.Missing[row.DML.SpaceID]++
			}
		}

		return nil
	}

	if len(rows) > 1 && !p.rowByRow {
		applied, err := p.applyInTransaction(txn, rows)
		if err != nil || applied {
			return err
		}
	}

	return p.applyRowByRow(txn, rows)
}

// applyRowByRow sends the rows as requests of their own, with up to Batch
// of them in flight. A transaction it has begun to send it sends whole, even
// when a refusal of an earlier one stops the play meanwhile.
func (p *player) applyRowByRow(txn *Txn, rows []*Row) error {
	state := &txnState{txn: txn, left: len(rows), applied: 0, failed: false}

	for index, row := range rows {
		for len(p.pending) >= p.opts.Batch {
			err := p.complete()
			if err != nil {
				return err
			}
		}

		if index == 0 && p.stopped {
			// A refusal stopped the play before the transaction began: it
			// is not gone through.
			p.summary.Txns--

			p.summary.Rows -= len(rows)

			return errStopped
		}

		future := p.target.doer.Do(newRowRequest(p.ctx, row))

		p.pending = append(p.pending, pendingRow{future: future, txn: state, row: row})
	}

	return nil
}

// complete waits for the oldest request in flight and accounts for it.
func (p *player) complete() error {
	head := p.pending[0]

	p.pending = p.pending[1:]

	_, err := head.future.Get()
	head.future.Release()

	state := head.txn
	state.left--

	switch {
	case err == nil:
		state.applied++

		p.summary.RowsApplied++
	case IsServerError(err):
		p.summary.RowsFailed++

		if !state.failed {
			state.failed = true
			p.fail(&RefusalError{
				File: state.txn.File, Replica: state.txn.Rows[0].ReplicaID,
				LSN: state.txn.Rows[0].LSN, Row: head.row, Err: err,
			})
		}
	default:
		return fmt.Errorf("applying the %s at LSN %d of replica %d of %s: %w",
			format.TypeName(head.row.Type), head.row.LSN, head.row.ReplicaID,
			state.txn.File.Path, err)
	}

	if state.left == 0 {
		p.finishTxn(state)
	}

	return nil
}

// finishTxn accounts for a transaction applied row by row.
func (p *player) finishTxn(state *txnState) {
	if state.failed {
		p.summary.TxnsFailed++
	} else {
		p.summary.TxnsApplied++
	}
}

// drain waits for every request in flight.
func (p *player) drain() error {
	for len(p.pending) > 0 {
		err := p.complete()
		if err != nil {
			return err
		}
	}

	return nil
}

// fail records a transaction the target refused.
func (p *player) fail(failure *RefusalError) {
	p.summary.Failures = append(p.summary.Failures, failure)

	if p.opts.OnError == OnErrorStop {
		p.stopped = true

		return
	}

	log.Errorf("Skipping %s", failure)
}

// applyInTransaction applies the rows in a stream transaction and reports
// whether it did, applied or refused. It returns false, to apply the rows
// row by row, when the target cannot take the transaction in auto mode.
func (p *player) applyInTransaction(txn *Txn, rows []*Row) (bool, error) {
	// A transaction starts once everything before it is through: a
	// refusal before it may stop the play.
	err := p.drain()
	if err != nil {
		return false, err
	}

	if p.stopped {
		return true, errStopped
	}

	// The rows are sent once the transaction has begun: after a BEGIN the
	// target refused they would be applied on their own.
	err = p.finish(tarantool.NewBeginRequest().Context(p.ctx))
	if err != nil {
		if !IsServerError(err) {
			return false, err
		}

		return p.notTaken(txn, rows, err, "the transaction could not begin")
	}

	futures := make([]tarantool.Future, 0, len(rows))
	for _, row := range rows {
		futures = append(futures, p.target.doer.Do(newRowRequest(p.ctx, row)))
	}

	var (
		refused    *Row
		refusedErr error
		other      error
	)

	for index, future := range futures {
		_, err := future.Get()
		future.Release()

		switch {
		case err == nil, refused != nil || other != nil:
		case IsServerError(err):
			refused, refusedErr = rows[index], err
		default:
			other = err
		}
	}

	if other != nil {
		return false, fmt.Errorf("applying the transaction at LSN %d of replica %d of %s: %w",
			txn.Rows[0].LSN, txn.Rows[0].ReplicaID, txn.File.Path, other)
	}

	if refused != nil {
		err := p.finish(tarantool.NewRollbackRequest().Context(p.ctx))
		if err != nil && !IsServerError(err) {
			return false, err
		}

		if isYield(refusedErr) {
			return p.notTaken(txn, rows, refusedErr, "a row was refused")
		}

		p.summary.RowsFailed++

		p.summary.RowsSkipped += len(rows) - 1
		p.summary.TxnsFailed++
		p.fail(&RefusalError{
			File: txn.File, Replica: txn.Rows[0].ReplicaID, LSN: txn.Rows[0].LSN,
			Row: refused, Err: refusedErr,
		})

		return true, nil
	}

	err = p.finish(tarantool.NewCommitRequest().Context(p.ctx))

	switch {
	case err == nil:
		p.summary.TxnsApplied++

		p.summary.RowsApplied += len(rows)
	case isYield(err):
		return p.notTaken(txn, rows, err, "the commit was refused")
	case IsServerError(err):
		p.summary.RowsSkipped += len(rows)
		p.summary.TxnsFailed++
		p.fail(&RefusalError{
			File: txn.File, Replica: txn.Rows[0].ReplicaID, LSN: txn.Rows[0].LSN,
			Row: nil, Err: err,
		})
	default:
		return false, err
	}

	return true, nil
}

// finish sends the commit or the rollback of a transaction and waits for it.
func (p *player) finish(request tarantool.Request) error {
	future := p.target.doer.Do(request)
	defer future.Release()

	_, err := future.Get()

	return err //nolint:wrapcheck // The caller describes the transaction.
}

// notTaken handles a transaction of rows the target could not take as a
// whole, which it rolled back: in auto mode the play goes on row by row from
// it, in stream mode the target refused it.
func (p *player) notTaken(txn *Txn, rows []*Row, cause error, what string) (bool, error) {
	if p.opts.TxnMode == TxnAuto {
		log.Warnf("The target cannot take a transaction of several rows over IPROTO (%s: %s); "+
			"applying the transactions row by row from the one at LSN %d of replica %d: "+
			"enable memtx_use_mvcc_engine on the target to apply them whole",
			what, cause, txn.Rows[0].LSN, txn.Rows[0].ReplicaID)

		p.rowByRow = true

		return false, nil
	}

	p.summary.RowsSkipped += len(rows)
	p.summary.TxnsFailed++
	p.fail(&RefusalError{
		File: txn.File, Replica: txn.Rows[0].ReplicaID, LSN: txn.Rows[0].LSN, Row: nil,
		Err: fmt.Errorf("%w; a transaction of several rows that changes a memtx space needs "+
			"memtx_use_mvcc_engine on the target", cause),
	})

	return true, nil
}

// IsServerError reports whether err is an error of the server, which
// refused a request, rather than one of the connection.
func IsServerError(err error) bool {
	var serverErr tarantool.ServerError

	return errors.As(err, &serverErr)
}

// isYield reports whether err is the error of a transaction a memtx space
// without MVCC aborted between two requests.
func isYield(err error) bool {
	var serverErr tarantool.ServerError

	return errors.As(err, &serverErr) && serverErr.Code == iproto.ER_TRANSACTION_YIELD
}

// rowRequest is the IPROTO request of a data change: its type and its body
// as the row holds them.
type rowRequest struct {
	ctx  context.Context //nolint:containedctx // A request carries its context.
	typ  iproto.Type
	body []byte
}

// requestKeys are the keys of a body of a data change a request carries;
// the others a journal row may hold, such as the old and the new tuple of
// the WAL extensions of Tarantool Enterprise, are not part of a request.
var requestKeys = []iproto.Key{
	iproto.IPROTO_SPACE_ID, iproto.IPROTO_INDEX_ID, iproto.IPROTO_INDEX_BASE,
	iproto.IPROTO_KEY, iproto.IPROTO_TUPLE, iproto.IPROTO_OPS, iproto.IPROTO_ARROW,
}

// newRowRequest makes the request of a data change from its row. The index
// and the base of the field numbers go with it: a DELETE or UPDATE of a
// secondary index looks its key up there, and the operations of an UPDATE
// or UPSERT from Lua count fields from 1, which the target rebases itself.
func newRowRequest(ctx context.Context, row *Row) rowRequest {
	dml := row.DML
	values := map[iproto.Key][]byte{
		iproto.IPROTO_KEY: dml.Key, iproto.IPROTO_TUPLE: dml.Tuple, iproto.IPROTO_OPS: dml.Ops,
		iproto.IPROTO_ARROW: dml.Arrow,
	}

	if dml.IndexID != 0 {
		values[iproto.IPROTO_INDEX_ID] = appendUint(nil, uint64(dml.IndexID))
	}

	if dml.IndexBase != 0 {
		values[iproto.IPROTO_INDEX_BASE] = appendUint(nil, uint64(dml.IndexBase))
	}

	var out []byte

	count := 1

	for _, key := range requestKeys[1:] {
		if values[key] != nil {
			count++
		}
	}

	out = appendMapLen(out, count)
	out = appendUint(out, uint64(iproto.IPROTO_SPACE_ID))
	out = appendUint(out, uint64(dml.SpaceID))

	for _, key := range requestKeys[1:] {
		if values[key] != nil {
			out = appendUint(out, uint64(key)) //nolint:gosec // An IPROTO key is not negative.
			out = append(out, values[key]...)
		}
	}

	return rowRequest{ctx: ctx, typ: row.Type, body: out}
}

// appendMapLen appends the MessagePack header of a map of size entries,
// size < 16.
func appendMapLen(dst []byte, size int) []byte {
	return append(dst, msgpcode.FixedMapLow|byte(size)) //nolint:gosec // A body has a few keys.
}

// appendUint appends value as a MessagePack unsigned integer.
func appendUint(dst []byte, value uint64) []byte {
	switch {
	case value <= math.MaxInt8: // A positive fixint, up to msgpcode.PosFixedNumHigh.
		return append(dst, byte(value))
	case value <= math.MaxUint8:
		return append(dst, msgpcode.Uint8, byte(value))
	case value <= math.MaxUint16:
		return binary.BigEndian.AppendUint16(append(dst, msgpcode.Uint16), uint16(value))
	case value <= math.MaxUint32:
		return binary.BigEndian.AppendUint32(append(dst, msgpcode.Uint32), uint32(value))
	default:
		return binary.BigEndian.AppendUint64(append(dst, msgpcode.Uint64), value)
	}
}

// Type returns the type of the data change.
func (r rowRequest) Type() iproto.Type { return r.typ }

// Body writes the body.
func (r rowRequest) Body(_ tarantool.SchemaResolver, enc *msgpack.Encoder) error {
	_, err := enc.Writer().Write(r.body)

	return err //nolint:wrapcheck // go-tarantool reports it.
}

// Ctx returns the context of the request.
func (r rowRequest) Ctx() context.Context { return r.ctx }

// Async reports that the request has an answer.
func (rowRequest) Async() bool { return false }

// Response decodes the answer.
func (rowRequest) Response(header tarantool.Header, body io.Reader) (tarantool.Response, error) {
	return tarantool.DecodeBaseResponse(header, body) //nolint:wrapcheck // go-tarantool's own.
}
