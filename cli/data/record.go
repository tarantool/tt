package data

import (
	"fmt"
	"maps"
	"slices"
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

// vylogKeyOrder is the order vy_log_record_encode in src/box/vy_log.c writes
// the keys of a vinyl log record in.
var vylogKeyOrder = []format.VyKey{
	format.VyKeyLSMID, format.VyKeyRangeID, format.VyKeyRunID, format.VyKeyBegin,
	format.VyKeyEnd, format.VyKeyIndexID, format.VyKeySpaceID, format.VyKeyGroupID,
	format.VyKeyDef, format.VyKeySliceID, format.VyKeyCreateLSN, format.VyKeyModifyLSN,
	format.VyKeyDropLSN, format.VyKeyDumpLSN, format.VyKeyGCLSN, format.VyKeyDumpCount,
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
	h := header(row)
	if isVinylRun(file.Kind) {
		h = vinylHeader(row)
	}

	fields := tuple.Map{{Key: fieldHeader, Value: h}}

	if body := Body(file, row); body != nil {
		fields = append(fields, tuple.MapItem{Key: fieldBody, Value: body})
	}

	return &Record{Row: row, File: file, Fields: fields}
}

// header returns the HEADER of a row: its type, LSN and replica always,
// its group and time when they are set, and, for a row of a transaction of
// several, the LSN of the first row of the transaction and, on its last row,
// commit.
func header(row *Row) tuple.Map {
	fields := tuple.Map{
		{Key: fieldType, Value: format.TypeName(row.Type)},
		{Key: fieldLSN, Value: row.LSN},
		{Key: fieldReplicaID, Value: uint64(row.ReplicaID)},
	}

	if row.GroupID != 0 {
		fields = append(fields, tuple.MapItem{Key: fieldGroupID, Value: uint64(row.GroupID)})
	}

	if multi := row.TSN != 0 && (row.TSN != row.LSN || !row.IsCommit()); multi {
		fields = append(fields, tuple.MapItem{Key: fieldTSN, Value: row.TSN})
		if row.IsCommit() {
			fields = append(fields, tuple.MapItem{Key: fieldCommit, Value: true})
		}
	}

	if row.Timestamp != 0 {
		fields = append(fields, tuple.MapItem{Key: fieldTimestamp, Value: row.Timestamp})
	}

	return fields
}

// Body returns the BODY of a row of file, nil for a row that has none. A key
// Tarantool has no name for is its number. A body that does not decode is
// its raw bytes, with the error.
func Body(file *File, row *Row) tuple.Map {
	kind := file.Kind

	if len(row.BodyRaw) == 0 || row.Type == iproto.IPROTO_NOP {
		return nil
	}

	if isVinylRun(kind) {
		return vinylBody(file, row)
	}

	switch {
	case kind == KindVylog:
		return vylogBody(row.BodyRaw)
	case isSynchro(row.Type):
		return synchroBody(row)
	}

	value, err := tuple.Decode(row.BodyRaw)
	if err != nil {
		return rawBody(row.BodyRaw, err)
	}

	body, ok := value.(tuple.Map)
	if !ok {
		return rawBody(row.BodyRaw, fmt.Errorf("%w: %T", errBodyNotMap, value))
	}

	if row.Type == iproto.IPROTO_RAFT {
		return nameKeys(body, func(key uint64) string { return raftKeys[key] }, raftValue)
	}

	return nameKeys(body, iprotoKey, nil)
}

// isSynchro reports whether t is a row of synchronous replication.
func isSynchro(t iproto.Type) bool {
	switch t {
	case iproto.IPROTO_RAFT_CONFIRM, iproto.IPROTO_RAFT_ROLLBACK, iproto.IPROTO_RAFT_PROMOTE,
		iproto.IPROTO_RAFT_DEMOTE:
		return true
	default:
		return false
	}
}

// synchroBody is the BODY of a row of synchronous replication: the owner of
// the queue and its LSN the row confirms or rolls back to and, of a PROMOTE
// or DEMOTE, the term and the confirmed vclock when the row has one.
func synchroBody(row *Row) tuple.Map {
	var (
		body    format.SynchroBody
		unknown map[uint64][]byte
	)

	err := format.DecodeSynchroBodyInto(row.BodyRaw, &body, format.CollectUnknown(&unknown))
	if err != nil {
		return rawBody(row.BodyRaw, err)
	}

	out := tuple.Map{
		{Key: "replica_id", Value: uint64(body.ReplicaID)},
		{Key: fieldLSN, Value: uint64(body.LSN)}, //nolint:gosec // An LSN is not negative.
	}

	if row.Type == iproto.IPROTO_RAFT_PROMOTE || row.Type == iproto.IPROTO_RAFT_DEMOTE {
		out = append(out, tuple.MapItem{Key: "term", Value: body.Term})
	}

	if body.ConfirmedVClock != nil {
		vclock := tuple.Map{}
		for _, id := range slices.Sorted(maps.Keys(body.ConfirmedVClock)) {
			vclock = append(vclock, tuple.MapItem{
				Key:   uint64(id),
				Value: uint64(body.ConfirmedVClock[id]), //nolint:gosec // An LSN.
			})
		}

		out = append(out, tuple.MapItem{Key: "vclock", Value: vclock})
	}

	return append(out, extras(unknown, iprotoKey)...)
}

// extras is the BODY of the keys a typed decoder does not know, by the names
// name gives them, in the order of the keys; a value that does not decode is
// its raw bytes.
func extras(raw map[uint64][]byte, name func(uint64) string) tuple.Map {
	out := make(tuple.Map, 0, len(raw))

	for _, key := range slices.Sorted(maps.Keys(raw)) {
		value, err := tuple.Decode(raw[key])
		if err != nil {
			value = tuple.Binary(raw[key])
		}

		var named any = key

		if text := name(key); text != "" {
			named = text
		}

		out = append(out, tuple.MapItem{Key: named, Value: value})
	}

	return out
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

// vylogBody is the BODY of a vinyl log row: the type of its record and its
// keys by name, in the order Tarantool writes them.
func vylogBody(raw []byte) tuple.Map {
	record, err := format.DecodeVyLogBody(raw)
	if err != nil {
		return rawBody(raw, err)
	}

	var typ any = uint64(record.Type)

	if name := record.Type.String(); !strings.HasPrefix(name, "VyLogType(") {
		typ = strings.ToLower(name)
	}

	out := tuple.Map{{Key: fieldType, Value: typ}}
	rest := maps.Clone(record.Keys)

	for _, key := range vylogKeyOrder {
		value, ok := rest[key]
		if !ok {
			continue
		}

		delete(rest, key)

		out = append(out, extras(map[uint64][]byte{uint64(key): value}, vylogKey)...)
	}

	unknown := make(map[uint64][]byte, len(rest))
	for key, value := range rest {
		unknown[uint64(key)] = value
	}

	return append(out, extras(unknown, vylogKey)...)
}
