package tuple_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"

	"github.com/tarantool/tt/sdk/tuple"
)

// uuidB64 is the base64 of the bytes of sampleUUID.
const uuidB64 = "tUFH/VOLTTmzrRe6cnYvSA=="

func TestAppendTaggedJSON(t *testing.T) {
	t.Parallel()

	got, err := tuple.AppendTaggedJSON(nil, decodeSample(t))
	require.NoError(t, err)
	assert.Equal(t, `[1,-2,18446744073709551615,-9223372036854775808,{"$float32":1.5},2.25,3.0,`+
		`{"$double":"NaN"},{"$double":"-Infinity"},"it's\n",{"$binary":"AAH/"},null,true,[],`+
		`{"$map":[["b",1],[2,"a"]]},{"$decimal":"-12.340"},{"$uuid":"`+sampleUUID+`"},`+
		`{"$datetime":"2024-11-14T14:02:36.027 Europe/Moscow"},`+
		`{"$interval":"+1 years, -2 seconds, adjust last"},{"$ext":{"type":66,"data":"Bw=="}}]`,
		string(got))

	// A map with a first key that looks like a tag is a $map, one with string
	// keys an object in the order of its keys, a Go map one in sorted order.
	for _, test := range []struct {
		value any
		want  string
	}{
		{"\xff", `{"$rawstring":"/w=="}`},
		{float32(math.NaN()), `{"$float32":"NaN"}`},
		{tuple.Map{{Key: "$uuid", Value: "x"}}, `{"$map":[["$uuid","x"]]}`},
		{tuple.Map{{Key: "b", Value: "$x"}, {Key: "$a", Value: uint64(1)}}, `{"b":"$x","$a":1}`},
		{map[string]any{"b": 1, "a": []int{1, 2}}, `{"a":[1,2],"b":1}`},
		{map[any]any{uint64(2): nil, "a": int8(1)}, `{"$map":[[2,null],["a",1]]}`},
		{(*decimal.Decimal)(nil), "null"},
	} {
		got, err := tuple.AppendTaggedJSON(nil, test.value)
		require.NoError(t, err)
		assert.Equal(t, test.want, string(got))
	}

	_, err = tuple.AppendTaggedJSON(nil, struct{}{})
	require.ErrorIs(t, err, tuple.ErrUnsupportedType)
}

func TestParseTaggedJSON(t *testing.T) {
	t.Parallel()

	items := decodeSample(t)

	text, err := tuple.AppendTaggedJSON(nil, items)
	require.NoError(t, err)

	back, err := tuple.ParseTaggedJSON(text)
	require.NoError(t, err)
	assertSameValue(t, items, back)

	moment, err := datetime.NewDatetime(time.Date(2024, 11, 14, 0, 0, 0, 0,
		time.FixedZone(datetime.NoTimezone, 0)))
	require.NoError(t, err)

	for _, test := range []struct {
		text string
		want any
	}{
		{` [ 1 , -1, 1.0, 1e2, 18446744073709551616 ] `,
			[]any{uint64(1), int64(-1), 1.0, 100.0, 1.8446744073709552e19}},
		{`{"b": 1, "a": {"$uuid": "` + sampleUUID + `"}}`,
			tuple.Map{{Key: "b", Value: uint64(1)}, {Key: "a", Value: uuid.MustParse(sampleUUID)}}},
		{`{"$map": [[1, 2], [1, 3]]}`,
			tuple.Map{{Key: uint64(1), Value: uint64(2)}, {Key: uint64(1), Value: uint64(3)}}},
		{`{"$datetime": "2024-11-14"}`, moment},
		{`{"$float32": 0.1}`, float32(0.1)},
		{`{"$double": "Infinity"}`, math.Inf(1)},
		{`{"$ext": {"data": "Bw==", "type": 66}}`, tuple.Ext{Type: 66, Data: []byte{7}}},
		{`{"$ext": {"type": 2, "data": "` + uuidB64 + `"}}`, uuid.MustParse(sampleUUID)},
		{`{"$rawstring": "/w=="}`, "\xff"},
		{`"é"`, "é"},
		{`"x \\ud800"`, `x \ud800`},
		{"\"\\" + "ud83d\\" + "ude00\"", "\U0001F600"},
		// Only an object whose only key is a tag is a tagged value.
		{`{"$nope": 1}`, tuple.Map{{Key: "$nope", Value: uint64(1)}}},
		{`{"$uuid": "x", "a": 1}`, tuple.Map{
			{Key: "$uuid", Value: "x"}, {Key: "a", Value: uint64(1)},
		}},
	} {
		got, err := tuple.ParseTaggedJSON([]byte(test.text))
		require.NoError(t, err, test.text)
		assertSameValue(t, test.want, got, test.text)
	}

	for _, text := range []string{
		``, `[1,`, `1 2`, `{"$uuid": "x"}`, `{"$double": "1"}`, `{"$float32": "x"}`,
		`{"$map": [[1]]}`, `{"$ext": {"type": 300, "data": ""}}`,
		`{"$binary": "!"}`, `{"$binary": 1}`, `{"$interval": "1 parsec"}`,
		strings.Repeat("[", 600) + strings.Repeat("]", 600),
		// Too small for the float, where 0 would be read.
		`1e-400`, `{"$float32": 1e-50}`,
		// encoding/json would read each of these strings as U+FFFD.
		"\"\xff\"", `["\ud800"]`, `"\udc00"`, `"\ud800x"`, `"\ud800\n"`, "{\"k\xff\": 1}",
	} {
		_, err := tuple.ParseTaggedJSON([]byte(text))
		require.ErrorIs(t, err, tuple.ErrTaggedJSON, text)
	}

	// As deep as Decode takes.
	_, err = tuple.ParseTaggedJSON([]byte(strings.Repeat("[", 500) + strings.Repeat("]", 500)))
	require.NoError(t, err)
}

func TestParseDatetime(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ text, want string }{
		{"2024-11-14", "2024-11-14T00:00:00Z"},
		{"2024-11-14T14:02", "2024-11-14T14:02:00Z"},
		{"2024-11-14 14:02:36", "2024-11-14T14:02:36Z"},
		{"2024-11-14t14:02:36.5z", "2024-11-14T14:02:36.500Z"},
		{"2024-11-14T14:02:36,123456+03:00", "2024-11-14T14:02:36.123456+0300"},
		{"2024-11-14T14:02:36.000000005-0130", "2024-11-14T14:02:36.000000005-0130"},
		{"2024-11-14T14:02:36+03", "2024-11-14T14:02:36+0300"},
		{"2024-11-14T14:02:36 Europe/Moscow", "2024-11-14T14:02:36 Europe/Moscow"},
		{"2024-11-14T14:02:36 MSK", "2024-11-14T14:02:36 MSK"},
		{"2024-11-14T14:02:36 UTC", "2024-11-14T14:02:36 UTC"},
		{"2024-11-14T14:02:36A", "2024-11-14T14:02:36A"},
		{"2024-11-14 Europe/Moscow", "2024-11-14T00:00:00 Europe/Moscow"},
		{"-001-01-01T00:00:00Z", "-001-01-01T00:00:00Z"},
		{"+12345-06-07T08:09:10+1400", "12345-06-07T08:09:10+1400"},
	} {
		value, err := tuple.ParseDatetime(test.text)
		require.NoError(t, err, test.text)

		got, _ := tuple.Text(value)
		assert.Equal(t, test.want, got, test.text)
	}

	for _, text := range []string{
		"", "2024", "2024-11", "24-11-14", "2024-13-01", "2024-02-30", "2024-11-14T25:00",
		"2024-11-14T14:02:60", "2024-11-14T14:02:36.1234567890", "2024-11-14T14:02:36+1500",
		"2024-11-14T14:02:36 Mars/Olympus", "2024-11-14T14:02:36 ", "2024-11-14T14:02:36a",
		"2024-11-14T14:02:36Zx", "2024-11-14T", "2024-11-14T14:02:36 Local",
	} {
		_, err := tuple.ParseDatetime(text)
		require.ErrorIs(t, err, tuple.ErrDatetime, text)
	}
}

func TestParseInterval(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		text string
		want datetime.Interval
	}{
		{"+0 seconds", datetime.Interval{}},
		{"0 seconds, adjust none", datetime.Interval{}},
		{"+1 years, -2 seconds", datetime.Interval{Year: 1, Sec: -2}},
		{"-2 seconds, 1 year", datetime.Interval{Year: 1, Sec: -2}},
		{"+1 weeks, 2 days, 3 hours, 4 minutes, 5 nanoseconds, adjust excess", datetime.Interval{
			Week: 1, Day: 2, Hour: 3, Min: 4, Nsec: 5, Adjust: datetime.ExcessAdjust,
		}},
		// The text of go-tupleconv, with the adjust mode as go-tarantool
		// numbers it.
		{"1,2,3,4,5,6,7,8,1", datetime.Interval{
			Year: 1, Month: 2, Week: 3, Day: 4, Hour: 5, Min: 6, Sec: 7, Nsec: 8,
			Adjust: datetime.ExcessAdjust,
		}},
	} {
		got, err := tuple.ParseInterval(test.text)
		require.NoError(t, err, test.text)
		assert.Equal(t, test.want, got, test.text)
	}

	for _, text := range []string{
		"", "1", "+1 parsecs", "+1 years, +2 years", "+x years", "adjust last, adjust none",
		"adjust sideways", "1,2,3,4,5,6,7,8,3", "+1 years,-2 seconds",
	} {
		_, err := tuple.ParseInterval(text)
		require.ErrorIs(t, err, tuple.ErrInterval, text)
	}
}

// FuzzParseField checks that the value ParseField reads from any text is
// written by AppendField as a text that reads back as it.
func FuzzParseField(f *testing.F) {
	for _, seed := range []string{
		"", "t", "1", "-0", "1.5", "1,5", "NaN", "true", `""`, `"\"\""`, "[1, {}]", `{"$map": []}`,
		`{"$decimal": "1.50"}`, sampleUUID, "2024-11-14T14:02:36 MSK", "+1 years",
		"1,0,0,0,0,0,0,0,0",
	} {
		f.Add(seed, uint8(0))
	}

	f.Fuzz(func(t *testing.T, text string, choice uint8) {
		typ := allFieldTypes[int(choice)%len(allFieldTypes)]
		field := tuple.Field{Name: "f", Type: typ, IsNullable: choice&0x80 != 0}
		opts := roundTripOptions[int(choice>>5)%len(roundTripOptions)]

		value, err := tuple.ParseField(text, field, opts)
		if err != nil {
			return
		}

		written, err := tuple.AppendField(nil, value, field, opts)
		require.NoError(t, err, "%q in a %s field: %#v", text, typ, value)

		back, err := tuple.ParseField(string(written), field, opts)
		require.NoError(t, err, "%q in a %s field: %q", text, typ, written)
		assertSameValue(t, value, back, "%q in a %s field: %q", text, typ, written)
	})
}
