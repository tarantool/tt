package data

import (
	"fmt"
	"strings"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"

	"github.com/tarantool/tt/sdk/tuple"
)

// Names of the fields of a record. A record is a map of a HEADER and, for a
// row that has one, a BODY, as Tarantool's xlog module reads a row.
const (
	fieldHeader    = "HEADER"
	fieldBody      = "BODY"
	fieldType      = "type"
	fieldLSN       = "lsn"
	fieldReplicaID = "replica_id"
	fieldGroupID   = "group_id"
	fieldTSN       = "tsn"
	fieldCommit    = "commit"
	fieldTimestamp = "timestamp"
	fieldRaw       = "raw"
	fieldError     = "error"
)

// Raft node states (raft_state in src/lib/raft/raft.h).
var raftStates = map[uint64]string{1: "follower", 2: "candidate", 3: "leader"}

// raftKeys names the keys of the body of a RAFT row.
var raftKeys = map[uint64]string{
	uint64(iproto.IPROTO_RAFT_TERM):           "term",
	uint64(iproto.IPROTO_RAFT_VOTE):           "vote",
	uint64(iproto.IPROTO_RAFT_STATE):          "state",
	uint64(iproto.IPROTO_RAFT_VCLOCK):         "vclock",
	uint64(iproto.IPROTO_RAFT_LEADER_ID):      "leader_id",
	uint64(iproto.IPROTO_RAFT_IS_LEADER_SEEN): "is_leader_seen",
}

// vylogTypes names the record types of a vinyl log (vy_log_type_name in
// src/box/vy_log.c).
var vylogTypes = []string{
	"create_lsm", "drop_lsm", "insert_range", "delete_range", "prepare_run",
	"create_run", "drop_run", "forget_run", "insert_slice", "delete_slice",
	"dump_lsm", "snapshot", "truncate_lsm", "modify_lsm", "forget_lsm",
	"prepare_lsm", "rebootstrap", "abort_rebootstrap",
}

// vylogKeys names the keys of a vinyl log record (vy_log_key_name in
// src/box/vy_log.c). go-xlog spells VY_LOG_KEY_DEF as DEF.
func vylogKey(key uint64) string {
	if key == uint64(format.VyKeyDef) {
		return "key_def"
	}

	name := format.VyKey(key).String()
	if strings.HasPrefix(name, "VyKey(") {
		return ""
	}

	return strings.ToLower(name)
}

// iprotoKey names a key of the body of a data change or of a synchronous
// replication row as Tarantool does: lower case, without the IPROTO_
// prefix, with IPROTO_OPS spelled "operations" as the xlog module of
// Tarantool spells it.
func iprotoKey(key uint64) string {
	if key == uint64(iproto.IPROTO_OPS) {
		return "operations"
	}

	name := iproto.Key(key).String() //nolint:gosec // A body key is a small protocol number.
	if !strings.HasPrefix(name, "IPROTO_") {
		return ""
	}

	return strings.ToLower(strings.TrimPrefix(name, "IPROTO_"))
}

// Record is a row as tt data cat prints it.
type Record struct {
	// Row is the row.
	Row *Row
	// File is the file of the row.
	File *File
	// Fields are the HEADER and the BODY.
	Fields tuple.Map
}

// NewRecord returns the record of a row of file.
func NewRecord(file *File, row *Row) *Record {
	fields := tuple.Map{{Key: fieldHeader, Value: header(row)}}

	if body := Body(file.Kind, row); body != nil {
		fields = append(fields, tuple.MapItem{Key: fieldBody, Value: body})
	}

	return &Record{Row: row, File: file, Fields: fields}
}

// header returns the HEADER of a row: its type, LSN and replica always,
// its group and time when they are set, and, for a row of a transaction of
// several, the LSN of the first row of the transaction and, on its last row,
// commit.
func header(row *Row) tuple.Map {
	h := tuple.Map{
		{Key: fieldType, Value: format.TypeName(row.Type)},
		{Key: fieldLSN, Value: row.LSN},
		{Key: fieldReplicaID, Value: uint64(row.ReplicaID)},
	}

	if row.GroupID != 0 {
		h = append(h, tuple.MapItem{Key: fieldGroupID, Value: uint64(row.GroupID)})
	}

	if multi := row.TSN != 0 && (row.TSN != row.LSN || !row.IsCommit()); multi {
		h = append(h, tuple.MapItem{Key: fieldTSN, Value: row.TSN})
		if row.IsCommit() {
			h = append(h, tuple.MapItem{Key: fieldCommit, Value: true})
		}
	}

	if row.Timestamp != 0 {
		h = append(h, tuple.MapItem{Key: fieldTimestamp, Value: row.Timestamp})
	}

	return h
}

// Body returns the BODY of a row of a file of kind, nil for a row that has
// none. A key Tarantool has no name for is its number. A body that does not
// decode is its raw bytes, with the error.
func Body(kind Kind, row *Row) tuple.Map {
	if len(row.BodyRaw) == 0 || row.Type == iproto.IPROTO_NOP {
		return nil
	}

	value, err := tuple.Decode(row.BodyRaw)
	if err != nil {
		return rawBody(row.BodyRaw, err)
	}

	body, ok := value.(tuple.Map)
	if !ok {
		return rawBody(row.BodyRaw, fmt.Errorf("the body is a %T, not a map", value))
	}

	switch {
	case kind == KindVylog:
		return vylogBody(body)
	case row.Type == iproto.IPROTO_RAFT:
		return nameKeys(body, func(key uint64) string { return raftKeys[key] }, raftValue)
	}

	return nameKeys(body, iprotoKey, nil)
}

func rawBody(raw []byte, err error) tuple.Map {
	return tuple.Map{
		{Key: fieldRaw, Value: tuple.Binary(raw)},
		{Key: fieldError, Value: err.Error()},
	}
}

// nameKeys replaces the integer keys of body with the names name gives
// them, and the values with what value makes of them, when value is set.
func nameKeys(body tuple.Map, name func(uint64) string,
	value func(name string, v any) any,
) tuple.Map {
	out := make(tuple.Map, 0, len(body))

	for _, item := range body {
		key, ok := item.Key.(uint64)
		if !ok {
			out = append(out, item)

			continue
		}

		named := name(key)
		if named == "" {
			out = append(out, item)

			continue
		}

		v := item.Value
		if value != nil {
			v = value(named, v)
		}

		out = append(out, tuple.MapItem{Key: named, Value: v})
	}

	return out
}

// raftValue names the state of a raft node.
func raftValue(name string, value any) any {
	if state, ok := value.(uint64); ok && name == "state" {
		if text, ok := raftStates[state]; ok {
			return text
		}
	}

	return value
}

// vylogBody is the BODY of a vinyl log row: its record, a tuple of the
// record type and a map of its keys, as a map of the type and the keys by
// name. A body of another shape is shown as it is.
func vylogBody(body tuple.Map) tuple.Map {
	named := nameKeys(body, iprotoKey, nil)

	for _, item := range named {
		if item.Key != "tuple" {
			continue
		}

		record, ok := item.Value.([]any)
		if !ok || len(record) != 2 { //nolint:mnd // A record is a type and a map.
			return named
		}

		typ, ok := record[0].(uint64)
		keys, isMap := record[1].(tuple.Map)

		if !ok || !isMap {
			return named
		}

		out := tuple.Map{{Key: fieldType, Value: vylogType(typ)}}

		return append(out, nameKeys(keys, vylogKey, nil)...)
	}

	return named
}

func vylogType(typ uint64) any {
	if typ < uint64(len(vylogTypes)) {
		return vylogTypes[typ]
	}

	return typ
}
