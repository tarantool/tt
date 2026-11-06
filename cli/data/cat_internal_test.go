package data

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/printing"
)

// dmlXlog writes an xlog with a data change of every kind, a transaction
// of two rows, synchronous replication rows and a RAFT row.
func dmlXlog(t *testing.T, dir string) string {
	t.Helper()

	key := uint64(iproto.IPROTO_KEY)
	tup := uint64(iproto.IPROTO_TUPLE)
	ops := uint64(iproto.IPROTO_OPS)
	space := uint64(iproto.IPROTO_SPACE_ID)
	index := uint64(iproto.IPROTO_INDEX_ID)
	base := uint64(iproto.IPROTO_INDEX_BASE)

	return writeJournal(t, dir, format.FiletypeXLOG, format.VClock{}, nil,
		tx(row(iproto.IPROTO_REPLACE, 1, 1, 1731592956.027, dml(t, 512, 1, "a"))),
		tx(row(iproto.IPROTO_INSERT, 1, 2, 1731592956.5, dml(t, 512, 2, []byte{0, 1})),
			row(iproto.IPROTO_DELETE, 1, 3, 1731592956.5, body(t, space, 512, index, 1,
				key, []any{"k"}))),
		// An update from Lua counts fields from 1, one from IPROTO from 0.
		tx(row(iproto.IPROTO_UPDATE, 1, 4, 1731592957, body(t, space, 512, base, 1,
			key, []any{1}, tup, []any{[]any{"=", 2, "x"}}))),
		tx(row(iproto.IPROTO_UPDATE, 1, 5, 1731592957, body(t, space, 512,
			key, []any{1}, tup, []any{[]any{"+", 1, 5}, []any{"=", -1, "y"},
				[]any{":", 3, 1, 0, "Q"}, []any{":", 3, -1, 0, "E"}}))),
		tx(row(iproto.IPROTO_UPSERT, 1, 6, 1731592957, body(t, space, 512, base, 1,
			tup, []any{1, "u"}, ops, []any{[]any{"=", 2, "z"}, []any{":", 3, 2, 0, "Q"}}))),
		tx(row(iproto.IPROTO_RAFT_PROMOTE, 1, 7, 1731592958, body(t,
			uint64(iproto.IPROTO_REPLICA_ID), 1, uint64(iproto.IPROTO_LSN), 6,
			uint64(iproto.IPROTO_TERM), 2, uint64(iproto.IPROTO_VCLOCK), map[uint64]int64{1: 6}))),
		tx(row(iproto.IPROTO_RAFT_CONFIRM, 1, 8, 1731592958, body(t,
			uint64(iproto.IPROTO_REPLICA_ID), 1, uint64(iproto.IPROTO_LSN), 6))),
		tx(row(iproto.IPROTO_RAFT, 0, 1, 1731592958, body(t,
			uint64(iproto.IPROTO_RAFT_TERM), 2, uint64(iproto.IPROTO_RAFT_VOTE), 1,
			uint64(iproto.IPROTO_RAFT_STATE), 3))),
		tx(row(iproto.IPROTO_NOP, 1, 9, 1731592958, nil)),
	)
}

func TestCatYAML(t *testing.T) {
	t.Parallel()

	path := dmlXlog(t, t.TempDir())
	filter := Filter{}

	got := cat(t, plan(t, &filter, path), &filter, printing.FormatYAML)
	assert.True(t, strings.HasPrefix(got, `---
HEADER:
  type: REPLACE
  lsn: 1
  replica_id: 1
  timestamp: 1731592956.027
BODY:
  space_id: 512
  tuple: [1, a]
---
HEADER:
  type: INSERT
  lsn: 2
  replica_id: 1
  tsn: 2
  timestamp: 1731592956.5
BODY:
  space_id: 512
  tuple: [2, !!binary AAE=]
---
HEADER:
  type: DELETE
  lsn: 3
  replica_id: 1
  tsn: 2
  commit: true
  timestamp: 1731592956.5
BODY:
  space_id: 512
  index_id: 1
  key: [k]
`), got)

	// Without --show-system the synchronous replication, RAFT and NOP rows
	// are left out.
	assert.NotContains(t, got, "RAFT")
	assert.NotContains(t, got, "NOP")
}

func TestCatSystemRowsDecoded(t *testing.T) {
	t.Parallel()

	path := dmlXlog(t, t.TempDir())
	filter := Filter{ShowSystem: true}

	got := cat(t, plan(t, &filter, path), &filter, output.FormatJSON)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	require.Len(t, lines, 10)

	assert.JSONEq(t, `{"HEADER":{"type":"RAFT_PROMOTE","lsn":7,"replica_id":1,`+
		`"timestamp":1731592958},"BODY":{"replica_id":1,"lsn":6,"term":2,"vclock":{"1":6}}}`,
		lines[6])
	assert.JSONEq(t, `{"HEADER":{"type":"RAFT_CONFIRM","lsn":8,"replica_id":1,`+
		`"timestamp":1731592958},"BODY":{"replica_id":1,"lsn":6}}`, lines[7])
	assert.JSONEq(t, `{"HEADER":{"type":"RAFT","lsn":1,"replica_id":0,`+
		`"timestamp":1731592958},"BODY":{"term":2,"vote":1,"state":"leader"}}`, lines[8])
	assert.JSONEq(t, `{"HEADER":{"type":"NOP","lsn":9,"replica_id":1,`+
		`"timestamp":1731592958}}`, lines[9])
}

func TestCatJSONLines(t *testing.T) {
	t.Parallel()

	path := dmlXlog(t, t.TempDir())
	filter := Filter{}

	got := cat(t, plan(t, &filter, path), &filter, output.FormatJSON)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	require.Len(t, lines, 6)

	// One object per line, the binary value as base64, the keys of a map
	// in their order.
	assert.Equal(t, `{"HEADER":{"type":"INSERT","lsn":2,"replica_id":1,"tsn":2,`+
		`"timestamp":1731592956.5},"BODY":{"space_id":512,"tuple":[2,"AAE="]}}`, lines[1])

	for _, line := range lines {
		var record map[string]map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		assert.Contains(t, record["HEADER"], "type")
		assert.Contains(t, record["HEADER"], "lsn")
		assert.Contains(t, record["HEADER"], "replica_id")
		assert.Contains(t, record["BODY"], "space_id")
	}
}

func TestCatLua(t *testing.T) {
	t.Parallel()

	path := dmlXlog(t, t.TempDir())
	filter := Filter{ShowSystem: true}

	got := cat(t, plan(t, &filter, path), &filter, FormatLua)
	assert.Equal(t, `box.space[512]:replace({1, 'a'})
box.space[512]:insert({2, require('varbinary').new('\x00\x01')})
box.space[512].index[1]:delete({'k'})
box.space[512]:update({1}, {{'=', 2, 'x'}})
box.space[512]:update({1}, {{'+', 2, 5}, {'=', -1, 'y'}, {':', 4, 2, 0, 'Q'}, {':', 4, -1, 0, 'E'}})
box.space[512]:upsert({1, 'u'}, {{'=', 2, 'z'}, {':', 3, 2, 0, 'Q'}})
`, got)
}

func TestCatVylog(t *testing.T) {
	t.Parallel()

	tup := uint64(iproto.IPROTO_TUPLE)
	path := writeJournal(t, t.TempDir(), format.FiletypeVYLOG, format.VClock{}, nil,
		tx(row(iproto.IPROTO_INSERT, 0, 1, 0, body(t, tup, []any{0, map[uint64]any{
			uint64(format.VyKeyLSMID): 1, uint64(format.VyKeySpaceID): 512,
			uint64(format.VyKeyDef): []any{[]any{0, "unsigned"}},
		}}))),
		tx(row(iproto.IPROTO_INSERT, 0, 2, 0, body(t, tup, []any{11, map[uint64]any{}}))),
	)

	filter := Filter{}

	got := cat(t, plan(t, &filter, path), &filter, output.FormatJSON)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	require.Len(t, lines, 2)

	var record struct {
		Body map[string]any `json:"BODY"`
	}

	require.NoError(t, json.Unmarshal([]byte(lines[0]), &record))
	assert.Equal(t, map[string]any{
		"type": "create_lsm", "lsm_id": float64(1), "space_id": float64(512),
		"key_def": []any{[]any{float64(0), "unsigned"}},
	}, record.Body)
	assert.Contains(t, lines[1], `"BODY":{"type":"snapshot"}`)

	// A vinyl log changes no space: it has no Lua.
	assert.Empty(t, cat(t, plan(t, &filter, path), &filter, FormatLua))
}

// TestCatInsertArrow pins that a batch of Arrow is a data change of its
// space: printed with its body, and named in the Lua output, which has no
// form for it, instead of being left out.
func TestCatInsertArrow(t *testing.T) {
	t.Parallel()

	arrow := msgpack.RawMessage{0xc7, 0x03, 0x08, 'A', 'B', 'C'}
	path := writeJournal(t, t.TempDir(), format.FiletypeXLOG, format.VClock{}, nil,
		tx(row(iproto.IPROTO_INSERT_ARROW, 1, 1, 1731592956, body(t,
			uint64(iproto.IPROTO_SPACE_ID), 512, uint64(iproto.IPROTO_ARROW), arrow))))
	filter := Filter{}

	got := cat(t, plan(t, &filter, path), &filter, output.FormatJSON)
	assert.Contains(t, got, `"type":"INSERT_ARROW"`)
	assert.Contains(t, got, `"space_id":512`)

	got = cat(t, plan(t, &filter, path), &filter, FormatLua)
	assert.Equal(t, "-- INSERT_ARROW into space 512 at LSN 1 of replica 1 has no Lua form\n", got)
}
