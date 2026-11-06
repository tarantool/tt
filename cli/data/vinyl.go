package data

import (
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-xlog/format"

	"github.com/tarantool/tt/sdk/tuple"
)

// vinylTypes names the row types of the files of a vinyl run as Tarantool
// does: the run info and the page infos of a .index file, the row index that
// ends each page of a .run file.
var vinylTypes = map[iproto.Type]string{
	iproto.VY_INDEX_RUN_INFO:  "RUNINFO",
	iproto.VY_INDEX_PAGE_INFO: "PAGEINFO",
	iproto.VY_RUN_ROW_INDEX:   "ROWINDEX",
}

// errBodyNotMap is the body of a row that is not a map.
var errBodyNotMap = errors.New("the body is not a map")

// stmtFlags names the flags of a statement of a vinyl run by bit
// (vy_stmt_flags in src/box/vy_stmt.h): deferred_delete on a REPLACE or
// DELETE of a primary index that left the old tuple in the secondary ones,
// skip_read on the DELETEs compaction generates for them.
var stmtFlags = []string{"deferred_delete", "skip_read"}

// Keys of the bodies of the rows of a vinyl run (src/box/iproto_constants.h).
const (
	runInfoStmtStat = 8
	rowIndexData    = 1
	stmtMetaFlags   = 1
)

// runInfoKeys names the keys of a run info (vy_run_info_key); the bloom
// filter, under the key of its version, is bloomKeys.
var runInfoKeys = map[uint64]string{
	1: "min_key", 2: "max_key", 3: "min_lsn", 4: "max_lsn", 5: "page_count",
	runInfoStmtStat: "stmt_stat",
}

// bloomKeys are the keys of the bloom filter of a run info and its version.
var bloomKeys = map[uint64]uint64{6: 1, 7: 2, 9: 3} //nolint:mnd // Versions.

// pageInfoKeys names the keys of a page info (vy_page_info_key).
var pageInfoKeys = map[uint64]string{
	1: "offset", 2: "size", 3: "unpacked_size", 4: "row_count", 5: "min_key",
	6: "row_index_offset",
}

// Names of the fields of the BODY of a vinyl run row that are not keys of it.
const (
	fieldSpaceID      = "space_id"
	fieldIndexID      = "index_id"
	fieldFlags        = "flags"
	fieldOffsets      = "offsets"
	fieldBloomVersion = "bloom_filter_version"
)

// typeName names a row type as Tarantool does.
func typeName(t iproto.Type) string {
	if name, ok := vinylTypes[t]; ok {
		return name
	}

	return format.TypeName(t)
}

// vinylHeader is the HEADER of a row of a vinyl run: its type and, for a
// statement, its LSN, the vclock signature of the transaction that wrote it.
// A row of a run has no replica, transaction or time.
func vinylHeader(row *Row) tuple.Map {
	h := tuple.Map{{Key: fieldType, Value: typeName(row.Type)}}
	if isStatement(row.Type) {
		h = append(h, tuple.MapItem{Key: fieldLSN, Value: row.LSN})
	}

	return h
}

// vinylBody is the BODY of a row of a vinyl run file: a statement with the
// space and the index its path names, and its flags by name; the offsets of
// a row index; a run info or page info by the names of its keys.
func vinylBody(file *File, row *Row) tuple.Map {
	value, err := tuple.Decode(row.BodyRaw)
	if err != nil {
		return rawBody(row.BodyRaw, err)
	}

	body, ok := value.(tuple.Map)
	if !ok {
		return rawBody(row.BodyRaw, fmt.Errorf("%w: %T", errBodyNotMap, value))
	}

	switch row.Type {
	case iproto.VY_INDEX_RUN_INFO:
		return runInfoBody(body)
	case iproto.VY_INDEX_PAGE_INFO:
		return nameKeys(sortedByKey(body), pageInfoKey, nil)
	case iproto.VY_RUN_ROW_INDEX:
		return rowIndexBody(body)
	default:
		return stmtBody(file, body)
	}
}

// pageInfoKey names a key of a page info.
func pageInfoKey(key uint64) string {
	return pageInfoKeys[key]
}

// stmtBody is the BODY of a statement of a vinyl run: the space and the index
// its path names, its tuple or key, its operations and its flags, in that
// order, and any other key after them.
func stmtBody(file *File, body tuple.Map) tuple.Map {
	out := tuple.Map{}
	if file.LSM != nil {
		out = append(out, tuple.MapItem{Key: fieldSpaceID, Value: uint64(file.LSM.SpaceID)},
			tuple.MapItem{Key: fieldIndexID, Value: uint64(file.LSM.IndexID)})
	}

	var rest tuple.Map

	for _, key := range []uint64{uint64(iproto.IPROTO_TUPLE), uint64(iproto.IPROTO_KEY),
		uint64(iproto.IPROTO_OPS), uint64(iproto.IPROTO_TUPLE_META)} {
		for _, item := range body {
			if item.Key != key {
				continue
			}

			meta, isMap := item.Value.(tuple.Map)

			switch {
			case key != uint64(iproto.IPROTO_TUPLE_META):
				out = append(out, tuple.MapItem{Key: iprotoKey(key), Value: item.Value})
			case isMap:
				for _, entry := range meta {
					if flags, ok := entry.Value.(uint64); ok && entry.Key == uint64(stmtMetaFlags) {
						out = append(out, tuple.MapItem{Key: fieldFlags, Value: flagNames(flags)})
					}
				}
			default:
				rest = append(rest, item)
			}
		}
	}

	for _, item := range nameKeys(body, iprotoKey, nil) {
		switch item.Key {
		case "tuple", "key", "operations", "tuple_meta":
		default:
			rest = append(rest, item)
		}
	}

	return append(out, rest...)
}

// flagNames names the flags of a statement; a flag with no name is its bit.
func flagNames(flags uint64) []any {
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

// runInfoBody is the BODY of a run info: its keys by name, in their order,
// the statement counts by type, and the version of the bloom filter in place
// of the filter.
func runInfoBody(body tuple.Map) tuple.Map {
	out := tuple.Map{}

	for _, item := range sortedByKey(body) {
		key, ok := item.Key.(uint64)
		if !ok {
			out = append(out, item)

			continue
		}

		if version, ok := bloomKeys[key]; ok {
			out = append(out, tuple.MapItem{Key: fieldBloomVersion, Value: version})

			continue
		}

		name, ok := runInfoKeys[key]
		if !ok {
			out = append(out, item)

			continue
		}

		value := item.Value
		if stat, ok := value.(tuple.Map); ok && key == runInfoStmtStat {
			value = nameKeys(sortedByKey(stat), func(t uint64) string {
				return strings.ToLower(typeName(iproto.Type(t))) //nolint:gosec // A small type.
			}, nil)
		}

		out = append(out, tuple.MapItem{Key: name, Value: value})
	}

	return out
}

// sortedByKey returns the items of m ordered by their integer keys, any other
// key after them.
func sortedByKey(m tuple.Map) tuple.Map {
	return slices.SortedStableFunc(slices.Values(m), func(a, b tuple.MapItem) int {
		x, okX := a.Key.(uint64)
		y, okY := b.Key.(uint64)

		switch {
		case okX && okY:
			return cmp.Compare(x, y)
		case okX:
			return -1
		case okY:
			return 1
		}

		return 0
	})
}

// rowIndexBody is the BODY of a row index: the offsets of the statements of
// the page in its rows.
func rowIndexBody(body tuple.Map) tuple.Map {
	for _, item := range body {
		data, ok := item.Value.(tuple.Binary)
		if item.Key != uint64(rowIndexData) || !ok || len(data)%4 != 0 {
			continue
		}

		offsets := make([]any, 0, len(data)/4) //nolint:mnd // An offset is a uint32.
		for i := 0; i < len(data); i += 4 {
			offsets = append(offsets, uint64(binary.BigEndian.Uint32(data[i:])))
		}

		return tuple.Map{{Key: fieldOffsets, Value: offsets}}
	}

	return nameKeys(body, func(uint64) string { return "" }, nil)
}

// luaRunStatement returns the box API statements that make what a statement
// of a vinyl run of a primary index holds, and nil for a row of a secondary
// index - its tuples are keys - or of a run whose path does not name its
// space and index. The operations of an UPSERT of a run are a list for each
// UPSERT squashed into it: it is an upsert of the tuple with the first list,
// which inserts the tuple when the key does not exist, and an upsert with
// each other list, which applies it.
func luaRunStatement(file *File, row *Row) []byte {
	if row.DML == nil || file.LSM == nil || file.LSM.IndexID != 0 || !isStatement(row.Type) {
		return nil
	}

	space := fmt.Appendf(nil, "box.space[%d]", file.LSM.SpaceID)
	body := row.DML

	switch row.Type { //nolint:exhaustive // An UPSERT is below, no other row is a statement.
	case iproto.IPROTO_INSERT:
		return appendLuaValue(append(space, ":insert("...), body.Tuple)
	case iproto.IPROTO_REPLACE:
		return appendLuaValue(append(space, ":replace("...), body.Tuple)
	case iproto.IPROTO_DELETE:
		return appendLuaValue(append(space, ":delete("...), body.Key)
	}

	value, err := tuple.Decode(body.Ops)
	lists, ok := value.([]any)

	if err != nil || !ok {
		out := appendLuaValue(append(space, ":upsert("...), body.Tuple)

		return appendLuaValue(append(out, ", "...), body.Ops)
	}

	var out []byte

	for i, list := range lists {
		if i > 0 {
			out = append(out, ")\n"...)
		}

		out = append(out, space...)
		out = append(out, ":upsert("...)
		out = appendLuaValue(out, body.Tuple)
		out = append(out, ", "...)
		out = appendOps(out, list, true)
	}

	return out
}
