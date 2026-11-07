package data

import (
	"errors"
	"fmt"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"

	"github.com/tarantool/tt/sdk/tuple"
)

// errBodyNotMap is the body of a row that is not a map.
var errBodyNotMap = errors.New("the body is not a map")

// stmtFlags names the flags of a statement of a vinyl run by bit
// (vy_stmt_flags in src/box/vy_stmt.h): deferred_delete on a REPLACE or
// DELETE of a primary index that left the old tuple in the secondary ones,
// skip_read on the DELETEs compaction generates for them.
var stmtFlags = []string{"deferred_delete", "skip_read"}

// Keys of a run info and of a page info (vy_run_info_key, vy_page_info_key in
// src/box/iproto_constants.h).
const (
	runInfoMinKey   = 1
	runInfoMaxKey   = 2
	pageInfoMinKey  = 5
	runInfoStmtStat = 8
)

// bloomFilter names the bloom filter of a run info, under the key of its
// version.
const bloomFilter = "bloom_filter"

// runInfoKeys and pageInfoKeys name the keys of a run info and a page info.
var (
	runInfoKeys = map[uint64]string{
		runInfoMinKey: "min_key", runInfoMaxKey: "max_key", 3: "min_lsn", 4: "max_lsn",
		5: "page_count", 6: bloomFilter, 7: bloomFilter, runInfoStmtStat: "stmt_stat",
		9: bloomFilter,
	}
	pageInfoKeys = map[uint64]string{
		1: "offset", 2: "size", 3: "unpacked_size", 4: "row_count", pageInfoMinKey: "min_key",
		6: "row_index_offset",
	}
)

// Names of the fields of the BODY of a vinyl run row that are not keys of it.
const (
	fieldSpaceID      = "space_id"
	fieldIndexID      = "index_id"
	fieldFlags        = "flags"
	fieldOffsets      = "offsets"
	fieldBloomVersion = "bloom_filter_version"
)

// vinylHeader is the HEADER of a row of a vinyl run: its type and, for a
// statement, its LSN, the vclock signature of the transaction that wrote it.
// A row of a run has no replica, transaction or time.
func vinylHeader(row *Row) tuple.Map {
	h := tuple.Map{{Key: fieldType, Value: format.TypeName(row.Type)}}
	if isStatement(row.Type) {
		h = append(h, tuple.MapItem{Key: fieldLSN, Value: row.LSN})
	}

	return h
}

// vinylBody is the BODY of a row of a vinyl run file: a statement with the
// space and the index its path names, and its flags by name; the offsets of
// a row index; a run info or page info by the names of its keys.
func vinylBody(file *File, row *Row) tuple.Map {
	switch row.Type {
	case iproto.VY_INDEX_RUN_INFO:
		return runInfoBody(row.BodyRaw)
	case iproto.VY_INDEX_PAGE_INFO:
		return pageInfoBody(row.BodyRaw)
	case iproto.VY_RUN_ROW_INDEX:
		return rowIndexBody(row.BodyRaw)
	default:
		return vinylStmtBody(file, row)
	}
}

// vinylStmtBody is the BODY of a statement of a vinyl run.
func vinylStmtBody(file *File, row *Row) tuple.Map {
	// The scan decoded the statement without the keys it has no field for:
	// decode it again to print them too.
	var (
		stmt    format.VyStmtBody
		unknown map[uint64][]byte
	)

	err := format.DecodeVyStmtBodyInto(row.BodyRaw, &stmt, format.CollectUnknown(&unknown))
	if err != nil {
		return rawBody(row.BodyRaw, err)
	}

	return stmtBody(file, &stmt, unknown)
}

// stmtBody is the BODY of a statement of a vinyl run: the space and the index
// its path names, its tuple or key, its operations and its flags, in that
// order, and the keys unknown to the decoder after them.
func stmtBody(file *File, stmt *format.VyStmtBody, unknown map[uint64][]byte) tuple.Map {
	out := tuple.Map{}
	if file.LSM != nil {
		out = append(out, tuple.MapItem{Key: fieldSpaceID, Value: uint64(file.LSM.SpaceID)},
			tuple.MapItem{Key: fieldIndexID, Value: uint64(file.LSM.IndexID)})
	}

	for _, field := range []struct {
		key uint64
		raw []byte
	}{
		{uint64(iproto.IPROTO_TUPLE), stmt.Tuple},
		{uint64(iproto.IPROTO_KEY), stmt.Key},
		{uint64(iproto.IPROTO_OPS), stmt.Ops},
	} {
		if field.raw != nil {
			out = append(out, extras(map[uint64][]byte{field.key: field.raw}, iprotoKey)...)
		}
	}

	if stmt.Flags != 0 {
		out = append(out, tuple.MapItem{Key: fieldFlags, Value: flagNames(stmt.Flags)})
	}

	if stmt.SpaceID != 0 {
		out = append(out, tuple.MapItem{
			Key: iprotoKey(uint64(iproto.IPROTO_SPACE_ID)), Value: uint64(stmt.SpaceID),
		})
	}

	return append(out, extras(unknown, iprotoKey)...)
}

// flagNames names the flags of a statement; a flag with no name is its bit.
func flagNames(flags format.VyStmtFlags) []any {
	names := []any{}

	for bit := range 64 {
		if flags&(1<<bit) == 0 {
			continue
		}

		if bit < len(stmtFlags) {
			names = append(names, stmtFlags[bit])
		} else {
			names = append(names, uint64(1)<<bit)
		}
	}

	return names
}

// rowIndexBody is the BODY of a row index: the offsets of the statements of
// the page in its rows.
func rowIndexBody(raw []byte) tuple.Map {
	offsets, err := format.DecodeVyRowIndexBody(raw)
	if err != nil {
		return rawBody(raw, err)
	}

	values := make([]any, 0, len(offsets))
	for _, offset := range offsets {
		values = append(values, uint64(offset))
	}

	return tuple.Map{{Key: fieldOffsets, Value: values}}
}

// runInfoBody is the BODY of a run info: its keys by name, the statement
// counts by type, and the version of the bloom filter in place of the
// filter.
func runInfoBody(raw []byte) tuple.Map {
	var (
		info    format.VyRunInfo
		unknown map[uint64][]byte
	)

	err := format.DecodeVyRunInfoBodyInto(raw, &info, format.CollectUnknown(&unknown))
	if err != nil {
		return rawBody(raw, err)
	}

	out := extras(map[uint64][]byte{runInfoMinKey: info.MinKey, runInfoMaxKey: info.MaxKey},
		runInfoKey)

	out = append(out,
		tuple.MapItem{Key: "min_lsn", Value: info.MinLSN},
		tuple.MapItem{Key: "max_lsn", Value: info.MaxLSN},
		tuple.MapItem{Key: "page_count", Value: uint64(info.PageCount)},
		tuple.MapItem{Key: runInfoKeys[runInfoStmtStat], Value: tuple.Map{
			{Key: "insert", Value: info.StmtStat.Inserts},
			{Key: "replace", Value: info.StmtStat.Replaces},
			{Key: "delete", Value: info.StmtStat.Deletes},
			{Key: "upsert", Value: info.StmtStat.Upserts},
		}},
	)

	if info.BloomVersion != 0 {
		out = append(out, tuple.MapItem{Key: fieldBloomVersion, Value: uint64(info.BloomVersion)})
	}

	return append(out, extras(unknown, runInfoKey)...)
}

// runInfoKey names a key of a run info.
func runInfoKey(key uint64) string {
	return runInfoKeys[key]
}

// pageInfoBody is the BODY of a page info: its keys by name.
func pageInfoBody(raw []byte) tuple.Map {
	var (
		page    format.VyPageInfo
		unknown map[uint64][]byte
	)

	err := format.DecodeVyPageInfoBodyInto(raw, &page, format.CollectUnknown(&unknown))
	if err != nil {
		return rawBody(raw, err)
	}

	out := tuple.Map{
		{Key: "offset", Value: page.Offset},
		{Key: "size", Value: uint64(page.Size)},
		{Key: "unpacked_size", Value: uint64(page.UnpackedSize)},
		{Key: "row_count", Value: uint64(page.RowCount)},
	}

	out = append(out, extras(map[uint64][]byte{pageInfoMinKey: page.MinKey}, pageInfoKey)...)
	out = append(out, tuple.MapItem{Key: "row_index_offset", Value: uint64(page.RowIndexOffset)})

	return append(out, extras(unknown, pageInfoKey)...)
}

// pageInfoKey names a key of a page info.
func pageInfoKey(key uint64) string {
	return pageInfoKeys[key]
}

// luaRunStatement returns the box API statements that make what a statement
// of a vinyl run of a primary index holds, and nil for a row of a secondary
// index - its tuples are keys - or of a run whose path does not name its
// space and index. The operations of an UPSERT of a run are a list for each
// UPSERT squashed into it: it is an upsert of the tuple with the first list,
// which inserts the tuple when the key does not exist, and an upsert with
// each other list, which applies it.
func luaRunStatement(file *File, row *Row) []byte {
	if row.Stmt == nil || file.LSM == nil || file.LSM.IndexID != 0 || !isStatement(row.Type) {
		return nil
	}

	space := fmt.Appendf(nil, "box.space[%d]", file.LSM.SpaceID)
	stmt := row.Stmt

	switch row.Type { //nolint:exhaustive // An UPSERT is below, no other row is a statement.
	case iproto.IPROTO_INSERT:
		return appendLuaValue(append(space, ":insert("...), stmt.Tuple)
	case iproto.IPROTO_REPLACE:
		return appendLuaValue(append(space, ":replace("...), stmt.Tuple)
	case iproto.IPROTO_DELETE:
		return appendLuaValue(append(space, ":delete("...), stmt.Key)
	}

	value, err := tuple.Decode(stmt.Ops)
	lists, ok := value.([]any)

	if err != nil || !ok {
		out := appendLuaValue(append(space, ":upsert("...), stmt.Tuple)

		return appendLuaValue(append(out, ", "...), stmt.Ops)
	}

	var out []byte

	for i, list := range lists {
		if i > 0 {
			out = append(out, ")\n"...)
		}

		out = append(out, space...)
		out = append(out, ":upsert("...)
		out = appendLuaValue(out, stmt.Tuple)
		out = append(out, ", "...)
		out = appendOps(out, list, true)
	}

	return out
}
