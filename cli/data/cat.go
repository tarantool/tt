package data

import (
	"fmt"
	"io"

	"github.com/tarantool/go-iproto"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/sdk/tuple"
)

// FormatLua is the format of tt data cat that writes the data changes as Lua
// statements of the box API.
const FormatLua output.Format = "lua"

// yamlIndent is tt's usual YAML indent.
const yamlIndent = 2

// MarshalJSON writes the record as one JSON object.
func (r *Record) MarshalJSON() ([]byte, error) {
	return tuple.AppendJSON(nil, r.Fields)
}

// MarshalYAML writes the record as a block mapping of its HEADER and BODY,
// the values in them in flow style, as Tarantool prints a tuple.
func (r *Record) MarshalYAML() (any, error) {
	return blockMapping(r.Fields, 2), nil //nolint:mnd // The record and its HEADER and BODY.
}

// blockMapping returns the node of m with depth levels of maps in block
// style and the values below them in flow style.
func blockMapping(m tuple.Map, depth int) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	for _, item := range m {
		value := tuple.YAMLNode(item.Value)
		if inner, ok := item.Value.(tuple.Map); ok && depth > 1 && len(inner) > 0 {
			value = blockMapping(inner, depth-1)
		}

		node.Content = append(node.Content, tuple.YAMLNode(item.Key), value)
	}

	return node
}

// Human writes the record as YAML.
func (r *Record) Human(w io.Writer) error {
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(yamlIndent)

	err := encoder.Encode(r)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	err = encoder.Close()
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	return nil
}

// CatOptions are how Cat reads and prints.
type CatOptions struct {
	ScanOptions
}

// Cat prints the rows of the transactions filter selects in plan to stream,
// one record each, and returns how many it printed.
func Cat(plan *Plan, filter *Filter, opts CatOptions, stream *output.Stream) (int, error) {
	printed := 0

	var current *File

	err := Scan(plan, filter, opts.ScanOptions, func(txn *Txn) error {
		if txn.File != current {
			current = txn.File
			log.Infof("Reading %s", current.Path)
		}

		for i := range txn.Rows {
			err := stream.Emit(NewRecord(txn.File, &txn.Rows[i]))
			if err != nil {
				return fmt.Errorf("printing the row at LSN %d of %s: %w",
					txn.Rows[i].LSN, txn.File.Path, err)
			}

			printed++
		}

		return nil
	})

	return printed, err
}

// luaEncoder writes the data changes of records as Lua statements, one per
// line; it writes nothing for a record of another row.
type luaEncoder struct {
	w io.Writer
}

// NewLuaStream is the stream encoder of FormatLua.
func NewLuaStream(w io.Writer) output.StreamEncoder {
	return luaEncoder{w: w}
}

// EncodeLua is the encoder of FormatLua.
func EncodeLua(w io.Writer, value any) error {
	return luaEncoder{w: w}.Encode(value)
}

// Encode writes the statement of a record.
func (e luaEncoder) Encode(item any) error {
	record, ok := item.(*Record)
	if !ok {
		return fmt.Errorf("lua: %T is not a journal record", item) //nolint:err113 // A programming error.
	}

	statement := LuaStatement(record.File.Kind, record.Row)
	if statement == nil {
		return nil
	}

	_, err := e.w.Write(append(statement, '\n'))
	if err != nil {
		return fmt.Errorf("writing Lua: %w", err)
	}

	return nil
}

// Close writes nothing: every statement is complete.
func (luaEncoder) Close() error {
	return nil
}

// LuaStatement returns the box API statement that makes the data change of
// row: box.space[512]:insert({...}), with .index[1] for an index other than
// the primary one. It returns nil for a row that is not a data change, and
// for the rows of a vinyl log, which change no space.
//
// The field numbers of the update operations of a row written through
// IPROTO, which counts fields from 0, are counted from 1, as Lua counts
// them.
func LuaStatement(kind Kind, row *Row) []byte {
	if row.DML == nil || kind == KindVylog {
		return nil
	}

	body := row.DML

	// A batch of Arrow has no Lua form: say so in its place rather than
	// leave it out without a word.
	if row.Type == iproto.IPROTO_INSERT_ARROW {
		return fmt.Appendf(nil, "-- INSERT_ARROW into space %d at LSN %d of replica %d has no Lua form",
			body.SpaceID, row.LSN, row.ReplicaID)
	}

	out := fmt.Appendf(nil, "box.space[%d]", body.SpaceID)

	if body.IndexID != 0 &&
		(row.Type == iproto.IPROTO_UPDATE || row.Type == iproto.IPROTO_DELETE) {
		out = fmt.Appendf(out, ".index[%d]", body.IndexID)
	}

	fromZero := body.IndexBase == 0

	switch row.Type {
	case iproto.IPROTO_INSERT:
		out = append(out, ":insert("...)
		out = appendLuaValue(out, body.Tuple)
	case iproto.IPROTO_REPLACE:
		out = append(out, ":replace("...)
		out = appendLuaValue(out, body.Tuple)
	case iproto.IPROTO_DELETE:
		out = append(out, ":delete("...)
		out = appendLuaValue(out, body.Key)
	case iproto.IPROTO_UPDATE:
		// An UPDATE carries its operations as IPROTO_TUPLE.
		out = append(out, ":update("...)
		out = appendLuaValue(out, body.Key)
		out = append(out, ", "...)
		out = appendLuaOps(out, body.Tuple, fromZero)
	case iproto.IPROTO_UPSERT:
		out = append(out, ":upsert("...)
		out = appendLuaValue(out, body.Tuple)
		out = append(out, ", "...)
		out = appendLuaOps(out, body.Ops, fromZero)
	default:
		return nil
	}

	return append(out, ')')
}

// appendLuaValue appends the Lua of a MessagePack value, nil for a value
// that does not decode.
func appendLuaValue(dst, raw []byte) []byte {
	if raw == nil {
		return append(dst, "{}"...)
	}

	value, err := tuple.Decode(raw)
	if err != nil {
		return append(dst, "nil --[[ "+err.Error()+" ]]"...)
	}

	return tuple.AppendLua(dst, value)
}

// appendLuaOps appends update operations, with their field numbers and the
// positions of their splices counted from 1 when fromZero says the row counts
// them from 0, as vinyl stores them. A negative number counts from the end
// either way.
func appendLuaOps(dst, raw []byte, fromZero bool) []byte {
	value, err := tuple.Decode(raw)
	if err != nil || !fromZero {
		return appendLuaValue(dst, raw)
	}

	ops, ok := value.([]any)
	if !ok {
		return tuple.AppendLua(dst, value)
	}

	for _, op := range ops {
		fields, ok := op.([]any)
		if !ok || len(fields) < 2 { //nolint:mnd // An operation and a field.
			continue
		}

		fields[1] = countedFromOne(fields[1])

		// A splice names the position in the string it cuts at the same
		// base as the field.
		if name, ok := fields[0].(string); ok && name == ":" &&
			len(fields) > 2 { //nolint:mnd // An operation, a field and a position.
			fields[2] = countedFromOne(fields[2])
		}
	}

	return tuple.AppendLua(dst, ops)
}

// countedFromOne returns a field number or a splice position counted from 0
// as counted from 1; a negative one, counted from the end, stays as it is.
func countedFromOne(number any) any {
	switch number := number.(type) {
	case uint64:
		return number + 1
	case int64:
		if number >= 0 {
			return number + 1
		}
	}

	return number
}
