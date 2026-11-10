package tuple_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/tuple"
)

// mustDecode decodes MessagePack that must decode.
func mustDecode(t *testing.T, raw []byte) any {
	t.Helper()

	value, err := tuple.Decode(raw)
	require.NoError(t, err)

	return value
}

// TestDecimalText checks that a decimal is the text Tarantool prints, which
// keeps its scale, and that a decimal Tarantool does not hold is no decimal.
func TestDecimalText(t *testing.T) {
	t.Parallel()

	decimalField := tuple.Field{Type: tuple.FieldDecimal}

	// The MessagePack Tarantool encodes decimal.new() of each text in.
	for _, test := range []struct{ raw, want string }{
		{"\xd5\x01\xfe\x1c", "1E+2"},
		{"\xc7\x03\x01\x00\x10\x0c", "100"},
		{"\xc7\x03\x01\x02\x15\x0c", "1.50"},
		{"\xd5\x01\x28\x1c", "1E-40"},
		{"\xd5\x01\x06\x1c", "0.000001"},
		{"\xd5\x01\x07\x1c", "1E-7"},
		{"\xc7\x03\x01\x09\x12\x3c", "1.23E-7"},
		{"\xc7\x03\x01\xfe\x01\x0c", "1.0E+3"},
		{"\xc7\x06\x01\xce\x00\x0f\x42\x40\x1c", "1E-1000000"},
	} {
		raw, want := test.raw, test.want

		value := mustDecode(t, []byte(raw))
		require.IsType(t, decimal.Decimal{}, value, want)

		text, _ := tuple.Text(value)
		assert.Equal(t, want, text)

		back, err := tuple.ParseField(text, decimalField, tuple.FieldOptions{})
		require.NoError(t, err)

		wantDecimal, _ := value.(decimal.Decimal)
		gotDecimal, _ := back.(decimal.Decimal)
		assert.Equal(t, wantDecimal.Exponent(), gotDecimal.Exponent(), want)
	}

	// A scale Tarantool refuses keeps the extension as it is, and its text is
	// short: a decimal is never written out digit by digit.
	value := mustDecode(t, []byte("\xc7\x06\x01\xd2\x7f\xff\xff\xff\x1c"))
	assert.IsType(t, tuple.Ext{}, value)

	for _, text := range []string{"1e2147483647", "1e-999999999", "1e1000000"} {
		_, err := tuple.ParseField(text, decimalField, tuple.FieldOptions{})
		require.ErrorIs(t, err, tuple.ErrFieldValue, text)
	}

	_, err := tuple.ParseTaggedJSON([]byte(`{"$decimal": "1e999999999"}`))
	require.ErrorIs(t, err, tuple.ErrTaggedJSON)
}

// TestTaggedJSONDepth checks that tagged JSON reads back a value as deep as
// Decode takes, whatever tags it nests, and that tags in a payload do not
// recurse.
func TestTaggedJSONDepth(t *testing.T) {
	t.Parallel()

	for _, raw := range [][]byte{
		append(bytes.Repeat([]byte{0x91}, 512), 0xc4, 0x00),
		append(bytes.Repeat([]byte{0x91}, 512), 0xd4, 0x42, 0x07),
		append(bytes.Repeat([]byte{0x81, 0x00}, 512), 0xc0),
	} {
		value := mustDecode(t, raw)

		text, err := tuple.AppendTaggedJSON(nil, value)
		require.NoError(t, err)

		back, err := tuple.ParseTaggedJSON(text)
		require.NoError(t, err)
		assertSameValue(t, value, back)
	}

	_, err := tuple.ParseTaggedJSON([]byte(strings.Repeat(`{"$decimal":`, 10000)))
	require.ErrorIs(t, err, tuple.ErrTaggedJSON)

	_, err = tuple.ParseTaggedJSON([]byte(strings.Repeat(`{"$map":[[`, 600) + "1"))
	require.ErrorIs(t, err, tuple.ErrTaggedJSON)
}

// TestDatetimeTextAmbiguous checks the datetimes whose Text, like the text
// of Tarantool, reads back as another one: both instants of an hour a zone
// repeats, a zone named Z, an offset the zone does not have.
func TestDatetimeTextAmbiguous(t *testing.T) {
	t.Parallel()

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone database")
	}

	// 02:30 on 2024-10-27 is in summer time, then an hour later again in
	// winter time. Which of the two the text alone reads back as is up to Go;
	// the other one has its offset in the text.
	summer, err := datetime.NewDatetime(time.Unix(1729989000, 0).In(berlin))
	require.NoError(t, err)

	winter, err := datetime.NewDatetime(time.Unix(1729992600, 0).In(berlin))
	require.NoError(t, err)

	summerJSON, err := tuple.AppendTaggedJSON(nil, summer)
	require.NoError(t, err)

	winterJSON, err := tuple.AppendTaggedJSON(nil, winter)
	require.NoError(t, err)
	assert.NotEqual(t, string(summerJSON), string(winterJSON))

	cases := []struct {
		moment time.Time
		tagged string
	}{
		{time.Unix(1729989000, 0).In(berlin), ""},
		{time.Unix(1729992600, 0).In(berlin), ""},
		{time.Date(2024, 11, 14, 0, 0, 0, 0, time.FixedZone("Z", 0)), "2024-11-14T00:00:00+0000 Z"},
		{
			time.Date(2024, 11, 14, 0, 0, 0, 0, time.FixedZone("MSK", 0)),
			"2024-11-14T00:00:00+0000 MSK",
		},
		{
			time.Date(2024, 11, 14, 0, 0, 0, 0, time.FixedZone("MSK", 3*3600)),
			"2024-11-14T00:00:00 MSK",
		},
	}

	for _, test := range cases {
		value, err := datetime.NewDatetime(test.moment)
		require.NoError(t, err)

		if test.tagged != "" {
			tagged, err := tuple.AppendTaggedJSON(nil, value)
			require.NoError(t, err)
			assert.Equal(t, `{"$datetime":"`+test.tagged+`"}`, string(tagged))
		}

		for _, typ := range []tuple.FieldType{tuple.FieldDatetime, tuple.FieldAny} {
			text, err := tuple.AppendField(nil, value, tuple.Field{Type: typ}, tuple.FieldOptions{})
			require.NoError(t, err)

			back, err := tuple.ParseField(string(text), tuple.Field{Type: typ},
				tuple.FieldOptions{})
			require.NoError(t, err)

			backDatetime, ok := back.(datetime.Datetime)
			require.True(t, ok, string(text))

			moment := backDatetime.ToTime()
			_, offset := moment.Zone()
			_, wantOffset := test.moment.Zone()

			assert.True(t, moment.Equal(test.moment), "%s: %s", text, moment)
			assert.Equal(t, wantOffset, offset, string(text))
			assert.Equal(t, test.moment.Location().String(), moment.Location().String())
		}
	}
}

// TestDatetimeAbbreviation checks that an abbreviation Tarantool knows is
// its fixed offset, as in Tarantool, even where the time zone database knows
// the name with summer time.
func TestDatetimeAbbreviation(t *testing.T) {
	t.Parallel()

	value, err := tuple.ParseDatetime("2024-07-01T12:00:00 CET")
	require.NoError(t, err)

	_, offset := value.ToTime().Zone()
	assert.Equal(t, 3600, offset)

	// go-tarantool decodes CET from the database, at +02:00 in July; the
	// text is the one Tarantool prints, at the offset of the payload.
	raw, err := value.MarshalMsgpack()
	require.NoError(t, err)

	decoded := mustDecode(t, tuple.AppendExtMsgpack(nil, tuple.ExtDatetime, raw))

	text, _ := tuple.Text(decoded)
	assert.Equal(t, "2024-07-01T12:00:00 CET", text)

	_, err = tuple.ParseDatetime("12345678-01-01")
	require.ErrorIs(t, err, tuple.ErrDatetime)
}

func TestNegativeZero(t *testing.T) {
	t.Parallel()

	value, err := tuple.ParseField("-0", tuple.Field{Type: tuple.FieldAny}, tuple.FieldOptions{})
	require.NoError(t, err)
	assert.Equal(t, uint64(0), value)

	value, err = tuple.ParseTaggedJSON([]byte("-0"))
	require.NoError(t, err)
	assert.Equal(t, uint64(0), value)
}

// TestGoMapOrder checks that a Go map with keys of the same text is written
// the same every time.
func TestGoMapOrder(t *testing.T) {
	t.Parallel()

	value := map[any]any{uint64(1): "a", "1": "b", int8(1): "c"}

	first, err := tuple.AppendTaggedJSON(nil, value)
	require.NoError(t, err)

	for range 50 {
		got, err := tuple.AppendTaggedJSON(nil, value)
		require.NoError(t, err)
		require.Equal(t, string(first), string(got))

		json, err := tuple.AppendJSON(nil, value)
		require.NoError(t, err)

		again, err := tuple.AppendJSON(nil, value)
		require.NoError(t, err)
		require.Equal(t, string(json), string(again)) //nolint:testifylint // The bytes count.
	}
}

func TestIntervalUnknownAdjust(t *testing.T) {
	t.Parallel()

	value := datetime.Interval{Year: 1, Adjust: 7}

	text, _ := tuple.Text(value)
	assert.Equal(t, "+1 years, adjust 7", text)

	back, err := tuple.ParseInterval(text)
	require.NoError(t, err)
	assert.Equal(t, value, back)
}

func TestFieldOptionsValidate(t *testing.T) {
	t.Parallel()

	for _, opts := range []tuple.FieldOptions{
		{Null: "\xff"},
		{ThousandSeparators: "0"},
		{ThousandSeparators: "e"},
		{DecimalSeparators: "-"},
		{ThousandSeparators: "\""},
		{ThousandSeparators: "{"},
		{ThousandSeparators: ",", DecimalSeparators: ","},
	} {
		require.ErrorIs(t, opts.Validate(), tuple.ErrFieldOptions, "%+v", opts)

		_, err := tuple.ParseField("1", tuple.Field{Type: tuple.FieldAny}, opts)
		require.ErrorIs(t, err, tuple.ErrFieldOptions)

		_, err = tuple.AppendField(nil, uint64(1), tuple.Field{Type: tuple.FieldAny}, opts)
		require.ErrorIs(t, err, tuple.ErrFieldOptions)
	}

	valid := tuple.FieldOptions{ThousandSeparators: " `'.", DecimalSeparators: ","}
	require.NoError(t, valid.Validate())
}

// TestAppendFieldSeparators checks that a number is written so that it reads
// back with a point for a thousand separator.
func TestAppendFieldSeparators(t *testing.T) {
	t.Parallel()

	opts := tuple.FieldOptions{ThousandSeparators: ".", DecimalSeparators: ","}

	for _, test := range []struct {
		value any
		typ   tuple.FieldType
		want  string
	}{
		{1.5, tuple.FieldDouble, "1,5"},
		{1.5, tuple.FieldAny, "1,5"},
		{decimal.MustNewDecimal("12.340"), tuple.FieldDecimal, "12,340"},
		{"1.5", tuple.FieldAny, `"1.5"`},
	} {
		field := tuple.Field{Type: test.typ}

		text, err := tuple.AppendField(nil, test.value, field, opts)
		require.NoError(t, err)
		assert.Equal(t, test.want, string(text))

		back, err := tuple.ParseField(string(text), field, opts)
		require.NoError(t, err)
		assertSameValue(t, test.value, back)
	}
}

// TestAppendFieldInvalidUTF8 checks the strings that are not UTF-8, which a
// quoted text could not hold.
func TestAppendFieldInvalidUTF8(t *testing.T) {
	t.Parallel()

	field := tuple.Field{Type: tuple.FieldString, IsNullable: true}

	for _, test := range []struct {
		value string
		null  string
	}{
		{"\xff", ""},
		{`"` + "\xff" + `"`, "�"},
		{"�", "�"},
	} {
		opts := tuple.FieldOptions{Null: test.null}

		text, err := tuple.AppendField(nil, test.value, field, opts)
		require.NoError(t, err)

		back, err := tuple.ParseField(string(text), field, opts)
		require.NoError(t, err)
		assert.Equal(t, test.value, back)
	}
}

func TestYAMLNodeStrings(t *testing.T) {
	t.Parallel()

	got, err := yaml.Marshal(tuple.Value{V: []any{"\xffa", "yes", "off", "x"}})
	require.NoError(t, err)
	assert.Equal(t, "[�a, \"yes\", \"off\", x]\n", string(got))

	// The strtod of C, and so Tarantool, reads these as numbers.
	got, err = yaml.Marshal(tuple.Value{V: []any{"nan", "-Inf", "Infinity", "0x1p4",
		"0x1.8p1", "1e400", "nan(1)", "1.", "inf2", "0x", "e5"}})
	require.NoError(t, err)
	assert.Equal(t, `["nan", "-Inf", "Infinity", "0x1p4", "0x1.8p1", "1e400", "nan(1)", `+
		`"1.", inf2, 0x, e5]`+"\n", string(got))
}

// TestYAMLStringsReadBackInTarantool checks that strings Tarantool could take
// for another type come back from its YAML as strings.
func TestYAMLStringsReadBackInTarantool(t *testing.T) {
	t.Parallel()

	tarantool, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found in PATH")
	}

	strs := []any{"nan", "NaN", "NAN", "inf", "Inf", "-inf", "+inf", "-nan", "Infinity",
		"-Infinity", "infinity", "1e400", "0x1p4", "0x1.8p1", "0x10", "12", "-1.5", ".5",
		"1.", "nan(1)", "true", "yes", "no", "null", "~", "<<", "x"}

	text, err := yaml.Marshal(tuple.Value{V: strs})
	require.NoError(t, err)

	dir := t.TempDir()
	input := filepath.Join(dir, "input.yaml")
	script := filepath.Join(dir, "read_back.lua")

	require.NoError(t, os.WriteFile(input, text, 0o600))
	require.NoError(t, os.WriteFile(script, []byte(`local f = io.open(arg[1])
local v = require('yaml').decode(f:read('*a'))
io.write((require('msgpack').encode(v):gsub('.', function(c)
    return string.format('%02x', c:byte())
end)))
os.exit(0)
`), 0o600))

	out, err := exec.CommandContext(t.Context(), tarantool, script, input).Output()
	require.NoError(t, err, "%s", text)
	assert.Equal(t, strs, decodeHex(t, strings.TrimSpace(string(out))), "%s", text)
}

func TestAppendLuaPointers(t *testing.T) {
	t.Parallel()

	dec := decimal.MustNewDecimal("1.5")

	assert.Equal(t, "require('decimal').new('1.5')", string(tuple.AppendLua(nil, &dec)))
	assert.Equal(t, "box.NULL", string(tuple.AppendLua(nil, (*decimal.Decimal)(nil))))

	moment, err := datetime.NewDatetime(time.Date(2024, 11, 14, 0, 0, 0, 0, time.FixedZone("Z", 0)))
	require.NoError(t, err)
	assert.Equal(t, "(require('datetime').parse('2024-11-14T00:00:00 Z'))",
		string(tuple.AppendLua(nil, moment)))
}

func TestGuessUUIDCanonical(t *testing.T) {
	t.Parallel()

	upper := strings.ToUpper(sampleUUID)

	value, err := tuple.ParseField(upper, tuple.Field{Type: tuple.FieldAny}, tuple.FieldOptions{})
	require.NoError(t, err)
	assert.Equal(t, upper, value)
}

// TestDecodeAllocation checks that a small value of deep arrays that claim
// many items takes memory by the bytes it has, not by the items it claims.
func TestDecodeAllocation(t *testing.T) {
	raw := append(bytes.Repeat([]byte{0xdc, 0xff, 0xff}, 600), make([]byte, 65535)...)

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)

	_, err := tuple.Decode(raw)
	require.ErrorIs(t, err, tuple.ErrInvalid)

	runtime.ReadMemStats(&after)

	const limit = 64 << 20

	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(limit))
}
