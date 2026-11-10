package tuple_test

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
	_ "github.com/tarantool/go-tarantool/v3/uuid"
	"github.com/vmihailenco/msgpack/v5"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/tuple"
)

const sampleUUID = "b54147fd-538b-4d39-b3ad-17ba72762f48"

func encode(t *testing.T, value any) []byte {
	t.Helper()

	b, err := msgpack.Marshal(value)
	require.NoError(t, err)

	return b
}

// sampleTuple is a tuple with a value of every kind Decode tells apart.
func sampleTuple(t *testing.T) []byte {
	t.Helper()

	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)

	moment, err := datetime.NewDatetime(time.Date(2024, 11, 14, 14, 2, 36, 27_000_000, moscow))
	require.NoError(t, err)

	head := []any{
		uint64(1), int64(-2), uint64(math.MaxUint64), int64(math.MinInt64),
		float32(1.5), 2.25, 3.0, math.NaN(), math.Inf(-1),
		"it's\n", []byte{0, 1, 0xff}, nil, true, []any{},
	}
	tail := []any{
		uuid.MustParse(sampleUUID), moment,
		datetime.Interval{Year: 1, Sec: -2, Adjust: datetime.LastAdjust},
	}

	// An array of 20 items. msgpack.Marshal has no ordered map, so the map
	// {"b": 1, 2: "a"} is written by hand, as is the decimal -12.340, which
	// go-tarantool would write as -12.34, and an extension of a type no
	// encoder knows.
	raw := []byte{0xdc, 0x00, 20}
	for _, v := range head {
		raw = append(raw, encode(t, v)...)
	}

	raw = append(raw, 0x82, 0xa1, 'b', 0x01, 0x02, 0xa1, 'a')
	raw = append(raw, 0xd6, byte(tuple.ExtDecimal), 0x03, 0x12, 0x34, 0x0d)

	for _, v := range tail {
		raw = append(raw, encode(t, v)...)
	}

	return append(raw, 0xd4, 0x42, 0x07)
}

func decodeSample(t *testing.T) []any {
	t.Helper()

	value, err := tuple.Decode(sampleTuple(t))
	require.NoError(t, err)

	items, ok := value.([]any)
	require.True(t, ok)

	return items
}

func TestDecode(t *testing.T) {
	t.Parallel()

	items := decodeSample(t)
	require.Len(t, items, 20)

	assert.Equal(t, uint64(1), items[0])
	assert.Equal(t, int64(-2), items[1])
	assert.Equal(t, uint64(math.MaxUint64), items[2])
	assert.Equal(t, int64(math.MinInt64), items[3])
	assert.IsType(t, float32(0), items[4])
	assert.InDelta(t, 1.5, items[4], 0)
	assert.InDelta(t, 2.25, items[5], 0)
	assert.Equal(t, "it's\n", items[9])
	assert.Equal(t, tuple.Binary{0, 1, 0xff}, items[10])
	assert.Nil(t, items[11])
	assert.Equal(t, true, items[12])
	assert.Equal(t, []any{}, items[13])
	assert.Equal(t, tuple.Map{{Key: "b", Value: uint64(1)}, {Key: uint64(2), Value: "a"}},
		items[14])
	assert.IsType(t, decimal.Decimal{}, items[15])
	assert.Equal(t, uuid.MustParse(sampleUUID), items[16])
	assert.IsType(t, datetime.Datetime{}, items[17])
	assert.Equal(t, datetime.Interval{Year: 1, Sec: -2, Adjust: datetime.LastAdjust}, items[18])
	assert.Equal(t, tuple.Ext{Type: 0x42, Data: []byte{0x07}}, items[19])
}

func TestDecodeErrors(t *testing.T) {
	t.Parallel()

	_, err := tuple.Decode([]byte{0x92, 0x01})
	require.ErrorIs(t, err, tuple.ErrTruncated)

	_, err = tuple.Decode([]byte{0x01, 0x02})
	require.ErrorIs(t, err, tuple.ErrTrailing)

	_, err = tuple.Decode([]byte{0xc1})
	require.ErrorIs(t, err, tuple.ErrInvalid)

	_, err = tuple.Decode([]byte{0xdd, 0xff, 0xff, 0xff, 0xff})
	require.ErrorIs(t, err, tuple.ErrTruncated)

	// A payload that does not decode keeps the extension as it is.
	value, err := tuple.Decode([]byte{0xd4, 0x02, 0x00})
	require.NoError(t, err)
	assert.Equal(t, tuple.Ext{Type: tuple.ExtUUID, Data: []byte{0}}, value)

	value, err = tuple.Decode([]byte{0xd4, 0x04, 0x00})
	require.NoError(t, err)
	assert.Equal(t, tuple.Ext{Type: tuple.ExtDatetime, Data: []byte{0}}, value)
}

func TestText(t *testing.T) {
	t.Parallel()

	items := decodeSample(t)

	want := map[int]string{
		0: "1", 1: "-2", 2: "18446744073709551615", 3: "-9223372036854775808",
		4: "1.5", 5: "2.25", 6: "3", 7: "NaN", 8: "-Infinity",
		9: "it's\n", 10: "AAH/", 12: "true",
		15: "-12.340", 16: sampleUUID, 17: "2024-11-14T14:02:36.027 Europe/Moscow",
		18: "+1 years, -2 seconds, adjust last", 19: "ext:66:Bw==",
	}

	for i, text := range want {
		got, ok := tuple.Text(items[i])
		assert.True(t, ok, "item %d", i)
		assert.Equal(t, text, got, "item %d", i)
	}

	for _, i := range []int{11, 13, 14} {
		_, ok := tuple.Text(items[i])
		assert.False(t, ok, "item %d", i)
	}

	got, ok := tuple.Text(int8(-3))
	assert.True(t, ok)
	assert.Equal(t, "-3", got)

	// A float has an exponent only where encoding/json would write one.
	for f, text := range map[float64]string{
		1731592956.818: "1731592956.818", 1e-7: "1e-7", 1e21: "1e+21", 0: "0",
	} {
		got, _ := tuple.Text(f)
		assert.Equal(t, text, got)
	}
}

func TestAppendJSON(t *testing.T) {
	t.Parallel()

	got, err := tuple.AppendJSON(nil, decodeSample(t))
	require.NoError(t, err)
	assert.JSONEq(t, `[1,-2,18446744073709551615,-9223372036854775808,1.5,2.25,3,"NaN",`+
		`"-Infinity","it's\n","AAH/",null,true,[],{"b":1,"2":"a"},"-12.340",`+
		`"`+sampleUUID+`","2024-11-14T14:02:36.027 Europe/Moscow",`+
		`"+1 years, -2 seconds, adjust last",`+
		`"ext:66:Bw=="]`, string(got))
	// The order of a map is kept.
	assert.Contains(t, string(got), `{"b":1,"2":"a"}`)

	got, err = tuple.AppendJSON(nil, "q\"\\\x01\xff é")
	require.NoError(t, err)
	assert.Equal(t, `"q\"\\\u0001`+"\\ufffd\\u2028é"+`"`, string(got))

	got, err = tuple.AppendJSON(nil, map[any]any{uint64(2): "b", "a": int8(1)})
	require.NoError(t, err)
	assert.Equal(t, `{"2":"b","a":1}`, string(got)) //nolint:testifylint // The key order counts.
}

func TestYAMLNode(t *testing.T) {
	t.Parallel()

	got, err := yaml.Marshal(tuple.Value{V: decodeSample(t)})
	require.NoError(t, err)
	assert.Equal(t, `[1, -2, 18446744073709551615, -9223372036854775808, 1.5, 2.25, 3.0, .nan, `+
		`-.inf, "it's\n", !!binary AAH/, null, true, [], {b: 1, 2: a}, "-12.340", `+
		sampleUUID+`, '2024-11-14T14:02:36.027 Europe/Moscow', `+
		`'+1 years, -2 seconds, adjust last', 'ext:66:Bw==']`+
		"\n", string(got))

	// A string that YAML would read as another type is quoted.
	got, err = yaml.Marshal(tuple.Value{V: []any{"true", "12", "null"}})
	require.NoError(t, err)
	assert.Equal(t, `["true", "12", "null"]`+"\n", string(got))
}

func TestAppendLua(t *testing.T) {
	t.Parallel()

	got := string(tuple.AppendLua(nil, decodeSample(t)))
	assert.Equal(t, `{1, -2, 18446744073709551615ULL, -9223372036854775808LL, `+
		`require('ffi').cast('float', 1.5), 2.25, require('ffi').cast('double', 3.0), `+
		`require('msgpack').object_from_raw('\xcb\x7f\xf8\x00\x00\x00\x00\x00\x01'), `+
		`-math.huge, 'it\'s\n', require('varbinary').new('\x00\x01\xff'), box.NULL, `+
		`true, setmetatable({}, {__serialize = 'seq'}), {['b'] = 1, [2] = 'a'}, `+
		`require('decimal').new('-12.340'), require('uuid').fromstr('`+sampleUUID+`'), `+
		`(require('datetime').parse('2024-11-14T14:02:36.027 Europe/Moscow')), `+
		`require('datetime').interval.new({year = 1, sec = -2, adjust = 'last'}), `+
		`require('msgpack').object_from_raw('\xd4B\x07')}`, got)

	assert.Equal(t, "setmetatable({}, {__serialize = 'map'})",
		string(tuple.AppendLua(nil, tuple.Map{})))
	assert.Equal(t, "'é\\x01'", string(tuple.AppendLua(nil, "é\x01")))
	assert.Equal(t, "'\\xc3'", string(tuple.AppendLua(nil, "\xc3")))
}

func TestAppendExtMsgpack(t *testing.T) {
	t.Parallel()

	for _, size := range []int{1, 2, 3, 4, 8, 16, 17, 300, 70000} {
		data := make([]byte, size)
		b := tuple.AppendExtMsgpack(nil, 0x42, data)

		value, err := tuple.Decode(b)
		require.NoError(t, err, "length %d", size)
		assert.Equal(t, tuple.Ext{Type: 0x42, Data: data}, value, "length %d", size)
	}
}
