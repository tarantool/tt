package xlog

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"

	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"
	"github.com/tarantool/go-xlog/writer"
)

const testUUID = "11111111-1111-1111-1111-111111111111"

// mkBody builds a minimal valid msgpack DML body: {IPROTO_TUPLE: [v]}.
func mkBody(t *testing.T, v uint64) []byte {
	t.Helper()

	buf := []byte{0x81, byte(iproto.IPROTO_TUPLE), 0x91}
	if v < 0x80 {
		return append(buf, byte(v))
	}

	return append(buf, 0xcf,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func row(t *testing.T, rep uint32, lsn int64) format.XRow {
	t.Helper()

	return format.XRow{
		Type:      iproto.IPROTO_INSERT,
		ReplicaID: rep,
		LSN:       lsn,
		BodyRaw:   mkBody(t, uint64(lsn)),
	}
}

// writeXlog creates a finalized journal at path with one tx per element of txs.
func writeXlog(t *testing.T, path string, meta *format.Meta, txs [][]format.XRow) {
	t.Helper()

	w, err := writer.Create(path, meta)
	require.NoError(t, err)

	for _, tx := range txs {
		require.NoError(t, w.WriteTx(tx))
	}

	require.NoError(t, w.Close())
}

func xlogMeta(t *testing.T, instUUID string, vclock, prev format.VClock) *format.Meta {
	t.Helper()

	id, err := uuid.Parse(instUUID)
	require.NoError(t, err)

	return &format.Meta{
		Filetype:     format.FiletypeXLOG,
		Version:      "tt-test/1.0",
		InstanceUUID: id,
		VClock:       vclock,
		PrevVClock:   prev,
	}
}

func snapMeta(t *testing.T, instUUID string, vclock format.VClock) *format.Meta {
	t.Helper()

	id, err := uuid.Parse(instUUID)
	require.NoError(t, err)

	return &format.Meta{
		Filetype:     format.FiletypeSNAP,
		Version:      "tt-test/1.0",
		InstanceUUID: id,
		VClock:       vclock,
	}
}

type rowKey struct {
	ReplicaID uint32
	LSN       int64
}

// readAllRows returns every row's (ReplicaID, LSN), asserting a clean,
// EOF-terminated file.
func readAllRows(t *testing.T, path string) []rowKey {
	t.Helper()

	r, err := reader.Open(path)
	require.NoError(t, err)

	defer func() { _ = r.Close() }()

	var out []rowKey

	for row, err := range r.Rows() {
		require.NoError(t, err)
		out = append(out, rowKey{ReplicaID: row.ReplicaID, LSN: row.LSN})
	}

	require.True(t, r.SawEOFMarker(), "file %s must end with a valid EOF marker", path)

	return out
}

func readMeta(t *testing.T, path string) *format.Meta {
	t.Helper()

	m, err := reader.ReadHeader(path)
	require.NoError(t, err)

	return m
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	return b
}

func tmpPath(t *testing.T, name string) string {
	t.Helper()

	return filepath.Join(t.TempDir(), name)
}

// writeChainFile writes one .xlog named after its vclock signature (the
// <signature>.xlog convention dir.OpenDir validates) and returns the path.
func writeChainFile(
	t *testing.T, dir string, vclock, prev format.VClock, txs [][]format.XRow,
) string {
	t.Helper()

	path := filepath.Join(dir, fmt.Sprintf("%020d.xlog", vclock.Signature()))
	writeXlog(t, path, xlogMeta(t, testUUID, vclock, prev), txs)

	return path
}
