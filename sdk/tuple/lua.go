package tuple

import (
	"math"
	"strconv"
	"strings"
	"time"
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
//   - A float is the shortest decimal that reads back as it, NaN is 0/0 and
//     an infinity math.huge or -math.huge.
//   - A string is a quoted Lua string, a [Binary] or a []byte a
//     require('varbinary').new() of one.
//   - An array is a Lua table of its items, a map a table of [key] = value
//     pairs; an empty one is marked with __serialize, so that it stays an
//     array or a map.
//   - A decimal, a UUID, a datetime and an interval are made by the decimal,
//     uuid and datetime modules; an [Ext] is made from its MessagePack by
//     the msgpack module, as a MessagePack object Tarantool stores as it is.
//
// A value of a type the package does not cover is written as a string of its
// text, or as nil when it has none.
//
//nolint:cyclop,funlen // One case per type is the clearest form.
func AppendLua(dst []byte, value any) []byte {
	switch typed := value.(type) {
	case nil:
		return append(dst, "box.NULL"...)
	case bool:
		return strconv.AppendBool(dst, typed)
	case string:
		return AppendLuaString(dst, typed)
	case Binary:
		return appendLuaBinary(dst, typed)
	case []byte:
		return appendLuaBinary(dst, typed)
	case float32:
		return appendLuaFloat(dst, float64(typed), float32Bits)
	case float64:
		return appendLuaFloat(dst, typed, float64Bits)
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
	case uuid.UUID:
		return appendLuaCall(dst, "require('uuid').fromstr(", typed.String())
	case datetime.Datetime:
		return appendLuaDatetime(dst, typed)
	case datetime.Interval:
		return appendLuaInterval(dst, typed)
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

func appendLuaFloat(dst []byte, number float64, bitSize int) []byte {
	switch {
	case math.IsNaN(number):
		return append(dst, "0/0"...)
	case math.IsInf(number, 1):
		return append(dst, "math.huge"...)
	case math.IsInf(number, -1):
		return append(dst, "-math.huge"...)
	}

	return append(dst, floatLiteral(number, bitSize)...)
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

func appendLuaMap(dst []byte, items Map) []byte {
	if len(items) == 0 {
		return append(dst, "setmetatable({}, {__serialize = 'map'})"...)
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

	return append(dst, '}')
}

// localLayout is RFC 3339 with nanoseconds and without the offset, which a
// time zone name follows instead.
const localLayout = "2006-01-02T15:04:05.999999999"

// appendLuaDatetime builds the datetime by datetime.parse, which reads back
// the instant to the nanosecond, with the time zone named when the datetime
// has one and its offset otherwise. datetime.new would not do: it takes a
// timestamp given with a time zone for the local time in it. The parentheses
// keep the length parse returns too out of a table constructor.
func appendLuaDatetime(dst []byte, d datetime.Datetime) []byte {
	t := d.ToTime()

	text := t.Format(time.RFC3339Nano)
	if zone := t.Location().String(); strings.Contains(zone, "/") {
		text = t.Format(localLayout) + " " + zone
	}

	dst = append(dst, "(require('datetime').parse("...)
	dst = AppendLuaString(dst, text)

	return append(dst, "))"...)
}

// luaAdjust names the adjust modes of an interval as Tarantool does.
var luaAdjust = map[datetime.Adjust]string{
	datetime.NoneAdjust:   "none",
	datetime.ExcessAdjust: "excess",
	datetime.LastAdjust:   "last",
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

	adjust, ok := luaAdjust[ival.Adjust]
	if !ok {
		adjust = luaAdjust[datetime.NoneAdjust]
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
