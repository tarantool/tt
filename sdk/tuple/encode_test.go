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

	"github.com/tarantool/tt/sdk/tuple"
)

// TestAppendMsgpackReadsBack encodes every value of the samples and decodes
// it back, and checks that the encoding of what it decodes is the same.
func TestAppendMsgpackReadsBack(t *testing.T) {
	t.Parallel()

	values := append(decodeSample(t), trickyValues(t)...)

	for _, value := range values {
		raw, err := tuple.AppendMsgpack(nil, value)
		require.NoError(t, err, "%#v", value)

		back := mustDecode(t, raw)
		assertSameValue(t, value, back, "%#v", value)

		again, err := tuple.AppendMsgpack(nil, back)
		require.NoError(t, err)
		assert.Equal(t, raw, again, "%#v", value)
	}
}

// TestAppendMsgpackBytes pins the MessagePack of values Tarantool encodes,
// the decimals as decimal.new() makes them.
func TestAppendMsgpackBytes(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"\x00", "\x7f", "\xcc\x80", "\xcd\x01\x00", "\xce\x00\x01\x00\x00",
		"\xcf\x00\x00\x00\x01\x00\x00\x00\x00", "\xff", "\xe0", "\xd0\xdf", "\xd1\xff\x7f",
		"\xd2\xff\xff\x7f\xff", "\xd3\xff\xff\xff\xff\x7f\xff\xff\xff",
		"\xca\x3f\xc0\x00\x00", "\xcb\x80\x00\x00\x00\x00\x00\x00\x00",
		"\xa0", "\xd9\x20" + string(make([]byte, 32)), "\xc4\x00", "\x90", "\x80",
		"\xd5\x01\xfe\x1c", "\xc7\x03\x01\x00\x10\x0c", "\xc7\x03\x01\x02\x15\x0c",
		"\xd5\x01\x28\x1c", "\xc7\x03\x01\xfe\x01\x0c", "\xc7\x06\x01\xce\x00\x0f\x42\x40\x1c",
		"\xd4\x01\x0c", "\xd4\x42\x07",
		"\x82\xa1b\x01\x02\xa1a", "\x82\xa1a\x01\xa1a\x02",
	} {
		got, err := tuple.AppendMsgpack(nil, mustDecode(t, []byte(raw)))
		require.NoError(t, err)
		assert.Equal(t, []byte(raw), got)
	}
}

// TestAppendMsgpackForeign checks the values go-tarantool decodes into.
func TestAppendMsgpackForeign(t *testing.T) {
	t.Parallel()

	dec := decimal.MustNewDecimal("1.50")
	moment, err := datetime.NewDatetime(time.Date(2024, 11, 14, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	for _, test := range []struct {
		value, want any
	}{
		{int8(-3), int64(-3)},
		{int(5), uint64(5)},
		{uint16(300), uint64(300)},
		{[]byte("ab"), tuple.Binary("ab")},
		{[]int{1, -2}, []any{uint64(1), int64(-2)}},
		{map[string]any{"b": 1, "a": 2}, tuple.Map{
			{Key: "a", Value: uint64(2)}, {Key: "b", Value: uint64(1)},
		}},
		{map[any]any{uint64(2): "x", "1": true}, tuple.Map{
			{Key: "1", Value: true}, {Key: uint64(2), Value: "x"},
		}},
		{&dec, dec},
		{(*decimal.Decimal)(nil), nil},
		{&moment, moment},
		{uuid.MustParse(sampleUUID), uuid.MustParse(sampleUUID)},
		{math.Inf(-1), math.Inf(-1)},
	} {
		raw, err := tuple.AppendMsgpack(nil, test.value)
		require.NoError(t, err, "%#v", test.value)
		assertSameValue(t, test.want, mustDecode(t, raw), "%#v", test.value)
	}

	prefix := []byte("pfx")

	got, err := tuple.AppendMsgpack(prefix, []any{1, struct{}{}})
	require.ErrorIs(t, err, tuple.ErrUnsupportedType)
	assert.Equal(t, prefix, got)

	_, err = tuple.AppendMsgpack(nil, decimal.MustNewDecimal("1e1000000"))
	require.Error(t, err)
}

// TestTaggedJSONDollarKeys checks the maps whose keys start with $: an object
// is a tagged value only if its only key is a tag.
func TestTaggedJSONDollarKeys(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value tuple.Map
		want  string
	}{
		{tuple.Map{{Key: "$a", Value: uint64(1)}}, `{"$a":1}`},
		{tuple.Map{{Key: "$decimal", Value: "1"}, {Key: "b", Value: uint64(2)}},
			`{"$decimal":"1","b":2}`},
		{tuple.Map{{Key: "$decimal", Value: "1"}}, `{"$map":[["$decimal","1"]]}`},
		{tuple.Map{{Key: "$map", Value: []any{}}}, `{"$map":[["$map",[]]]}`},
	} {
		text, err := tuple.AppendTaggedJSON(nil, test.value)
		require.NoError(t, err)
		assert.Equal(t, test.want, string(text))

		back, err := tuple.ParseTaggedJSON(text)
		require.NoError(t, err)
		assertSameValue(t, test.value, back)

		field, err := tuple.ParseField(test.want, tuple.Field{Type: tuple.FieldMap},
			tuple.FieldOptions{})
		require.NoError(t, err)
		assertSameValue(t, test.value, field)
	}

	// The JSON an older export wrote, its keys sorted.
	value, err := tuple.ParseField(`{"$id": 1, "name": "x"}`, tuple.Field{Type: tuple.FieldAny},
		tuple.FieldOptions{})
	require.NoError(t, err)
	assertSameValue(t, tuple.Map{{Key: "$id", Value: uint64(1)}, {Key: "name", Value: "x"}}, value)
}

func TestGuessLegacyInfinities(t *testing.T) {
	t.Parallel()

	anyField := tuple.Field{Type: tuple.FieldAny}

	for text, want := range map[string]float64{
		"+Inf": math.Inf(1), "Inf": math.Inf(1), "-Inf": math.Inf(-1),
	} {
		value, err := tuple.ParseField(text, anyField, tuple.FieldOptions{})
		require.NoError(t, err)
		assert.Equal(t, want, value) //nolint:testifylint // An infinity is exact.
	}

	value, err := tuple.ParseField("inf", anyField, tuple.FieldOptions{})
	require.NoError(t, err)
	assert.Equal(t, "inf", value)

	text, err := tuple.AppendField(nil, "+Inf", anyField, tuple.FieldOptions{})
	require.NoError(t, err)
	assert.Equal(t, `"+Inf"`, string(text))
}

func TestRawVarbinary(t *testing.T) {
	t.Parallel()

	field := tuple.Field{Type: tuple.FieldVarbinary, IsNullable: true}
	raw := tuple.FieldOptions{RawVarbinary: true}

	for _, value := range []tuple.Binary{
		tuple.Binary("test"), {0, 0xff, '"'}, {}, tuple.Binary(`""`),
	} {
		text, err := tuple.AppendField(nil, value, field, raw)
		require.NoError(t, err)

		back, err := tuple.ParseField(string(text), field, raw)
		require.NoError(t, err)
		assert.Equal(t, value, back)
	}

	text, err := tuple.AppendField(nil, tuple.Binary("test"), field, raw)
	require.NoError(t, err)
	assert.Equal(t, "test", string(text))

	// Base64 in the quotes of JSON, as the JSON of a []byte is.
	value, err := tuple.ParseField(`"YWJj"`, field, tuple.FieldOptions{})
	require.NoError(t, err)
	assert.Equal(t, tuple.Binary("abc"), value)
}

func TestLoose(t *testing.T) {
	t.Parallel()

	loose := tuple.FieldOptions{Loose: true}

	for _, test := range []struct {
		value any
		field tuple.Field
		want  string
	}{
		{"abc", tuple.Field{Type: tuple.FieldVarbinary}, "YWJj"},
		{"x", tuple.Field{Type: tuple.FieldUnsigned}, "x"},
		{"12", tuple.Field{Type: tuple.FieldUnsigned}, `"12"`},
		{nil, tuple.Field{Type: tuple.FieldUnsigned}, ""},
		{uint64(1), tuple.Field{Type: "decimal1024"}, "1"},
	} {
		_, err := tuple.AppendField(nil, test.value, test.field, tuple.FieldOptions{})
		require.Error(t, err)

		text, err := tuple.AppendField(nil, test.value, test.field, loose)
		require.NoError(t, err, "%#v", test.value)
		assert.Equal(t, test.want, string(text))
	}

	value, err := tuple.ParseField("12", tuple.Field{Type: "decimal1024"}, loose)
	require.NoError(t, err)
	assert.Equal(t, uint64(12), value)
}

func TestFieldDefault(t *testing.T) {
	t.Parallel()

	for _, field := range []tuple.Field{
		{Type: tuple.FieldUnsigned, Default: uint64(5)},
		{Type: tuple.FieldUnsigned, DefaultFunc: "next_id"},
	} {
		value, err := tuple.ParseField("", field, tuple.FieldOptions{})
		require.NoError(t, err)
		assert.Nil(t, value)
	}
}
