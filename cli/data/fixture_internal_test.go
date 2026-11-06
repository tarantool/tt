package data

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/writer"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/printing"
)

// fixtureUUID is the instance UUID of the fixture files.
var fixtureUUID = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// body encodes a row body: a map of integer keys, in the order given.
func body(t *testing.T, pairs ...any) []byte {
	t.Helper()

	var buf bytes.Buffer

	enc := msgpack.NewEncoder(&buf)
	require.NoError(t, enc.EncodeMapLen(len(pairs)/2))

	for i := 0; i < len(pairs); i += 2 {
		require.NoError(t, enc.Encode(pairs[i]))
		require.NoError(t, enc.Encode(pairs[i+1]))
	}

	return buf.Bytes()
}

// dml is the body of a data change of space with the tuple under
// IPROTO_TUPLE.
func dml(t *testing.T, space uint32, tuple ...any) []byte {
	t.Helper()

	return body(t, uint64(iproto.IPROTO_SPACE_ID), space, uint64(iproto.IPROTO_TUPLE), tuple)
}

// row is a row of a write-ahead log.
func row(typ iproto.Type, replica uint32, lsn int64, ts float64, b []byte) format.XRow {
	return format.XRow{Type: typ, ReplicaID: replica, LSN: lsn, Timestamp: ts, BodyRaw: b}
}

// insert is an INSERT of tuple into space by replica at lsn.
func insert(t *testing.T, replica uint32, lsn int64, space uint32, tuple ...any) format.XRow {
	t.Helper()

	return row(iproto.IPROTO_INSERT, replica, lsn, 1000+float64(lsn), dml(t, space, tuple...))
}

// writeJournal writes a journal file of kind into dir, named after the
// signature of vclock as Tarantool names it, with the transactions txs.
func writeJournal(t *testing.T, dir string, filetype format.Filetype, vclock,
	prev format.VClock, txs ...[]format.XRow,
) string {
	t.Helper()

	ext, err := filetype.Ext()
	require.NoError(t, err)

	path := filepath.Join(dir, fmt.Sprintf("%020d%s", vclock.Signature(), ext))

	w, err := writer.Create(path, &format.Meta{
		Filetype:     filetype,
		InstanceUUID: fixtureUUID,
		VClock:       vclock,
		PrevVClock:   prev,
	}, writer.Version("3.7.0"))
	require.NoError(t, err)

	for _, tx := range txs {
		require.NoError(t, w.WriteTx(tx))
	}

	require.NoError(t, w.Close())

	return path
}

// tx is a transaction of rows.
func tx(rows ...format.XRow) []format.XRow { return rows }

// plan collects and selects paths with filter.
func plan(t *testing.T, filter *Filter, paths ...string) *Plan {
	t.Helper()

	files, err := Collect(paths, false)
	require.NoError(t, err)

	p, err := Select(files, filter)
	require.NoError(t, err)

	return p
}

// scanned is a transaction Scan selected: its file and the replica and LSN
// of its rows.
type scanned struct {
	file string
	rows []string
}

// scan runs Scan and returns what it selected.
func scan(t *testing.T, p *Plan, filter *Filter) []scanned {
	t.Helper()

	var out []scanned

	require.NoError(t, Scan(p, filter, ScanOptions{}, func(txn *Txn) error {
		s := scanned{file: filepath.Base(txn.File.Path)}
		for _, r := range txn.Rows {
			s.rows = append(s.rows, fmt.Sprintf("%d:%d", r.ReplicaID, r.LSN))
		}

		out = append(out, s)

		return nil
	}))

	return out
}

// lsns returns the rows of what scan returned, transaction by transaction,
// as "replica:lsn" joined by commas.
func lsns(txns []scanned) []string {
	out := make([]string, 0, len(txns))
	for _, txn := range txns {
		out = append(out, strings.Join(txn.rows, ","))
	}

	return out
}

// cat runs Cat into a buffer in format.
func cat(t *testing.T, p *Plan, filter *Filter, format output.Format) string {
	t.Helper()

	var out bytes.Buffer

	opts := append(printing.Options(), output.WithEncoder(FormatLua, EncodeLua),
		output.WithStreamEncoder(FormatLua, NewLuaStream))

	printer, err := output.NewPrinter(output.Streams{In: nil, Out: &out, Err: io.Discard},
		format, opts...)
	require.NoError(t, err)

	stream, err := printer.Stream()
	require.NoError(t, err)

	_, err = Cat(p, filter, CatOptions{ScanOptions: ScanOptions{}}, stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())

	return out.String()
}
