// Package tuple decodes the MessagePack values Tarantool stores - tuples,
// keys, update operations, the fields of a journal row - renders them as
// text: JSON, YAML and Lua, and writes and reads them as the text of the
// fields of a space with no loss, as the export and import of data do.
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
// extension in the form Tarantool prints it, its adjust mode added to an
// interval. [AppendJSON] writes JSON in which every value that has no JSON
// type is a string: its [Text] - a decimal, a UUID, a datetime, an
// interval, an [Ext] - or, for a [Binary], its base64; a map key that is not
// a string is its [Text] as well, and a float that is NaN or infinite is
// "NaN", "Infinity" or "-Infinity", as in
// [github.com/tarantool/tt/sdk/output]. [YAMLNode] builds a YAML node, in
// flow style, as Tarantool prints a tuple, with a binary value tagged
// !!binary. [AppendLua] writes a Lua expression that builds the value in
// Tarantool: an extension through the module that makes it, a binary value
// through the varbinary module, nil as box.NULL.
//
// [AppendMsgpack] is the inverse of [Decode]: it writes the MessagePack that
// Decode reads back, a decimal with its scale, a [Map] in its order, so that
// a value read from text can be stored.
//
// [AppendTaggedJSON] writes JSON that loses nothing: a value JSON has no type
// for is an object of one key that names its type, such as {"$decimal":
// "12.340"}, a map with a key that is not a string {"$map": [[key, value],
// ...]}. [ParseTaggedJSON] reads it back; an object of any other key, or of
// more keys, is a map.
//
// [AppendField] writes a value as the text of a [Field] of a space, as a cell
// of CSV holds it, and [ParseField] reads it back, by the type of the field:
// a value is its [Text] when that reads back as the same value, its tagged
// JSON otherwise, so that a string "12" in a field of type any is "\"12\"".
// [ParseField] reads the text people write as well - thousand and decimal
// separators, a guess of the type of a value of an any or scalar field - and
// the text github.com/tarantool/go-tupleconv writes. [FieldOptions] may
// make a varbinary field raw bytes rather than base64, and make AppendField
// loose: write a value of a type its field does not hold rather than fail.
// [ParseDatetime] and [ParseInterval] read the text of a datetime and of an
// interval.
//
// One loss is left: a decimal is a decimal.Decimal, which has no -0, so the
// -0 and -0.00 Tarantool can store are 0 and 0.00, equal to them in
// Tarantool, but written with the sign dropped.
//
// The API of this package is alpha and may change incompatibly.
package tuple
