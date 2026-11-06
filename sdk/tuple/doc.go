// Package tuple decodes the MessagePack values Tarantool stores - tuples,
// keys, update operations, the fields of a journal row - and renders them as
// text: JSON, YAML and Lua.
//
// [Decode] turns MessagePack into Go values that keep what MessagePack tells
// apart and Go does not:
//
//   - MP_NIL is nil, MP_BOOL a bool.
//   - MP_UINT is a uint64 and MP_INT an int64, whatever their width on the
//     wire; MP_FLOAT is a float32 and MP_DOUBLE a float64.
//   - MP_STR is a string and MP_BIN a [Binary].
//   - MP_ARRAY is a []any and MP_MAP a [Map], which keeps the order of its
//     entries.
//   - The extensions Tarantool defines are the types of go-tarantool: a
//     decimal is a decimal.Decimal, a UUID a uuid.UUID, a datetime a
//     datetime.Datetime and an interval a datetime.Interval. Any other
//     extension - an error, a compressed value, one this package does not
//     know - and an extension whose payload does not decode is an [Ext], its
//     type and payload as they are.
//
// The renderers take these values, and the values go-tarantool decodes a
// response into as well: integers and floats of any width, []byte,
// map[any]any and map[string]any, whose keys they sort, as Go maps have no
// order.
//
// [Text] is the text of a scalar: a number in decimal, a string as it is, an
// extension in the form Tarantool prints it. [AppendJSON] writes JSON in
// which every value that has no JSON type is a string: its [Text] - a
// decimal, a UUID, a datetime in RFC 3339, an interval, an [Ext] - or, for a
// [Binary], its base64; a map key that is not a string is its [Text] as
// well, and a float that is NaN or infinite is "NaN", "Infinity" or
// "-Infinity", as in [github.com/tarantool/tt/sdk/output]. [YAMLNode] builds
// a YAML node, in flow style, as Tarantool prints a tuple, with a binary
// value tagged !!binary. [AppendLua] writes a Lua expression that builds the
// value in Tarantool: an extension through the module that makes it, a
// binary value through the varbinary module, nil as box.NULL.
//
// The API of this package is alpha and may change incompatibly.
package tuple
