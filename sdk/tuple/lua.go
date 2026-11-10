package tuple

import (
	"math"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
)

// maxExactInt is the largest integer a Lua number holds exactly: a larger
// one is written as a 64-bit integer literal of LuaJIT.
const maxExactInt = 1 << 53

// AppendLua appends a Lua expression that builds value in Tarantool:
//
//   - nil is box.NULL, which a Tarantool table keeps where nil would end an
//     array or vanish from a map.
//   - An integer beyond 2^53 is a LuaJIT 64-bit literal, such as
//     18446744073709551615ULL, as a Lua number would lose its low digits.
//   - A float is the shortest decimal that reads back as it, and an infinity
//     math.huge or -math.huge. An integral float64, -0 included, is cast to a
//     C double by the ffi module and a float32 to a C float, which Tarantool
//     encodes as MP_DOUBLE and MP_FLOAT, where it would encode a Lua number
//     of an integral value as an integer. A NaN is made from its MessagePack,
//     which keeps its sign and payload: 0/0 is a NaN of the sign the CPU
//     gives it.
//   - A string is a quoted Lua string, a [Binary] or a []byte a
//     require('varbinary').new() of one.
//   - An array is a Lua table of its items, a map a table of [key] = value
//     pairs; an empty array, an empty map and a map of keys 1, 2... are
//     marked with __serialize, so that each stays what it is. Keys Lua takes
//     for one, such as an MP_INT 1 and an MP_UINT 1, are one key in the
//     table.
//   - A decimal, a UUID, a datetime and an interval are made by the decimal,
//     uuid and datetime modules; an [Ext] is made from its MessagePack by
//     the msgpack module, as a MessagePack object Tarantool stores as it is.
//
// A value of a type the package does not cover is written as a string of its
// text, or as nil when it has none. Lua cannot build what Tarantool may
// store: a map with a NaN key, a value nested deeper than LuaJIT parses -
// some 200 levels - or an interval of an adjust mode Tarantool has no name
// for, which is written with none.
//
//nolint:cyclop,funlen // One case per type is the clearest form.
func AppendLua(dst []byte, value any) []byte {
	if isNil(value) {
		return append(dst, "box.NULL"...)
	}

	switch typed := value.(type) {
	case bool:
		return strconv.AppendBool(dst, typed)
	case string:
		return AppendLuaString(dst, typed)
	case Binary:
		return appendLuaBinary(dst, typed)
	case []byte:
		return appendLuaBinary(dst, typed)
	case float32:
		return appendLuaFloat32(dst, typed)
	case float64:
		return appendLuaFloat64(dst, typed)
	case []any:
		if len(typed) == 0 {
			return append(dst, "setmetatable({}, {__serialize = 'seq'})"...)
		}

		dst = append(dst, '{')

		for i, item := range typed {
			if i > 0 {
				dst = append(dst, ", "...)
			}

			dst = AppendLua(dst, item)
		}

		return append(dst, '}')
	case Map:
		return appendLuaMap(dst, typed)
	case map[any]any:
		return appendLuaMap(dst, sortedMap(typed))
	case map[string]any:
		items := make(Map, 0, len(typed))
		for key, item := range typed {
			items = append(items, MapItem{Key: key, Value: item})
		}

		return appendLuaMap(dst, sortItems(items))
	case decimal.Decimal:
		return appendLuaCall(dst, "require('decimal').new(", decimalText(typed))
	case *decimal.Decimal:
		return appendLuaCall(dst, "require('decimal').new(", decimalText(*typed))
	case uuid.UUID:
		return appendLuaCall(dst, "require('uuid').fromstr(", typed.String())
	case datetime.Datetime:
		return appendLuaDatetime(dst, typed)
	case *datetime.Datetime:
		return appendLuaDatetime(dst, *typed)
	case datetime.Interval:
		return appendLuaInterval(dst, typed)
	case *datetime.Interval:
		return appendLuaInterval(dst, *typed)
	case Ext:
		return appendLuaCall(dst, "require('msgpack').object_from_raw(",
			string(AppendExtMsgpack(nil, typed.Type, typed.Data)))
	}

	if i, ok := asInt(value); ok {
		dst = strconv.AppendInt(dst, i, decimalBase)
		if i > maxExactInt || i < -maxExactInt {
			dst = append(dst, "LL"...)
		}

		return dst
	}

	if u, ok := asUint(value); ok {
		dst = strconv.AppendUint(dst, u, decimalBase)
		if u > maxExactInt {
			dst = append(dst, "ULL"...)
		}

		return dst
	}

	if text, ok := Text(value); ok {
		return AppendLuaString(dst, text)
	}

	return append(dst, "nil"...)
}

// appendLuaFloat32 appends a float32 cast to a C float, or a NaN as its
// MessagePack.
func appendLuaFloat32(dst []byte, number float32) []byte {
	if math.IsNaN(float64(number)) {
		return appendLuaRaw(dst, number)
	}

	dst = append(dst, "require('ffi').cast('float', "...)

	return append(appendLuaFloat(dst, float64(number), float32Bits), ')')
}

// appendLuaFloat64 appends a float64 as a Lua number, an integral one cast to
// a C double, or a NaN as its MessagePack.
func appendLuaFloat64(dst []byte, number float64) []byte {
	switch {
	case math.IsNaN(number):
		return appendLuaRaw(dst, number)
	case number == math.Trunc(number) && !math.IsInf(number, 0):
		dst = append(dst, "require('ffi').cast('double', "...)

		return append(appendLuaFloat(dst, number, float64Bits), ')')
	}

	return appendLuaFloat(dst, number, float64Bits)
}

// appendLuaFloat appends a float that is not a NaN as a Lua number.
func appendLuaFloat(dst []byte, number float64, bitSize int) []byte {
	switch {
	case math.IsInf(number, 1):
		return append(dst, "math.huge"...)
	case math.IsInf(number, -1):
		return append(dst, "-math.huge"...)
	}

	return append(dst, floatLiteral(number, bitSize)...)
}

// appendLuaRaw appends a float as a MessagePack object of its bytes, which
// Tarantool stores as they are.
func appendLuaRaw(dst []byte, value any) []byte {
	raw, err := AppendMsgpack(nil, value)
	if err != nil {
		return append(dst, "nil"...)
	}

	return appendLuaCall(dst, "require('msgpack').object_from_raw(", string(raw))
}

func appendLuaBinary(dst []byte, b []byte) []byte {
	return appendLuaCall(dst, "require('varbinary').new(", string(b))
}

// appendLuaCall appends call, the string argument and a closing parenthesis.
func appendLuaCall(dst []byte, call, arg string) []byte {
	dst = append(dst, call...)
	dst = AppendLuaString(dst, arg)

	return append(dst, ')')
}

// appendLuaMap appends a map as a table of its entries, marked with
// __serialize where Tarantool would take it for an array otherwise: when it
// is empty, or all its keys are Lua numbers that are positive integers. A
// mark on every map would make deep values too complex an expression for
// LuaJIT sooner.
func appendLuaMap(dst []byte, items Map) []byte {
	mark := !slices.ContainsFunc(items, func(item MapItem) bool {
		return !isLuaIndex(item.Key)
	})

	if mark {
		dst = append(dst, "setmetatable("...)
	}

	dst = append(dst, '{')

	for i, item := range items {
		if i > 0 {
			dst = append(dst, ", "...)
		}

		dst = append(dst, '[')
		dst = AppendLua(dst, item.Key)
		dst = append(dst, "] = "...)
		dst = AppendLua(dst, item.Value)
	}

	if mark {
		return append(dst, "}, {__serialize = 'map'})"...)
	}

	return append(dst, '}')
}

// isLuaIndex reports whether AppendLua writes a key as a Lua number that is a
// positive integer, which Tarantool takes for an index of an array: an
// integer up to 2^53, math.huge. An integral float64 is a C double, which is
// not one.
func isLuaIndex(key any) bool {
	if unsigned, ok := asUint(key); ok {
		return unsigned > 0 && unsigned <= maxExactInt
	}

	if signed, ok := asInt(key); ok {
		return signed > 0 && signed <= maxExactInt
	}

	number, ok := key.(float64)

	return ok && math.IsInf(number, 1)
}

// appendLuaDatetime builds the datetime by datetime.parse, which reads back
// its Text: the instant to the nanosecond and its time zone. datetime.new
// would not do: it takes a timestamp given with a time zone for the local
// time in it. parse takes a Z after the time for the zone Z, which a military
// one-letter zone is written as after a space, so a datetime of no zone is
// written with its offset, +0000 where Text has the Z.
// Like Tarantool, parse does not tell the two instants of an hour a zone
// repeats apart, and it does not take an ambiguous abbreviation, such as PST,
// which is written as its offset alone. The parentheses keep the length parse
// returns too out of a table constructor.
func appendLuaDatetime(dst []byte, value datetime.Datetime) []byte {
	moment := value.ToTime()
	text := datetimeText(value)

	switch zone := moment.Location().String(); {
	case len(zone) == 1:
		text = text[:len(text)-1] + " " + zone
	case zone == datetime.NoTimezone, ambiguousZoneAbbrevs[zone]:
		_, offset := moment.Zone()

		text = string(appendOffset(appendClock(nil, moment), offset))
	}

	dst = append(dst, "(require('datetime').parse("...)
	dst = AppendLuaString(dst, text)

	return append(dst, "))"...)
}

func appendLuaInterval(dst []byte, ival datetime.Interval) []byte {
	dst = append(dst, "require('datetime').interval.new({"...)

	fields := []struct {
		name  string
		value int64
	}{
		{"year", ival.Year}, {"month", ival.Month}, {"week", ival.Week},
		{"day", ival.Day}, {"hour", ival.Hour}, {"min", ival.Min},
		{"sec", ival.Sec}, {"nsec", ival.Nsec},
	}

	for _, field := range fields {
		if field.value == 0 {
			continue
		}

		dst = append(dst, field.name...)
		dst = append(dst, " = "...)
		dst = strconv.AppendInt(dst, field.value, decimalBase)
		dst = append(dst, ", "...)
	}

	adjust, ok := adjustNames[ival.Adjust]
	if !ok {
		adjust = adjustNames[datetime.NoneAdjust]
	}

	dst = append(dst, "adjust = "...)
	dst = AppendLuaString(dst, adjust)

	return append(dst, "})"...)
}

// AppendLuaString appends text as a single-quoted Lua string. Printable ASCII
// and, when text is valid UTF-8, the characters beyond it are written as they
// are; any other byte is a \x escape.
func AppendLuaString(dst []byte, text string) []byte {
	keepUnicode := utf8.ValidString(text)

	dst = append(dst, '\'')

	for i := range len(text) {
		char := text[i]

		switch {
		case char == '\'' || char == '\\':
			dst = append(dst, '\\', char)
		case char == '\n':
			dst = append(dst, '\\', 'n')
		case char == '\r':
			dst = append(dst, '\\', 'r')
		case char == '\t':
			dst = append(dst, '\\', 't')
		case char >= 0x20 && char < 0x7f, char >= utf8.RuneSelf && keepUnicode:
			dst = append(dst, char)
		default:
			dst = append(dst, '\\', 'x', hexDigits[char>>4], hexDigits[char&0xf])
		}
	}

	return append(dst, '\'')
}
