package tuple_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"

	"github.com/tarantool/tt/sdk/tuple"
)

// allFieldTypes are the field types the package knows.
var allFieldTypes = []tuple.FieldType{
	tuple.FieldAny, tuple.FieldScalar, tuple.FieldString, tuple.FieldBoolean,
	tuple.FieldUnsigned, tuple.FieldInteger, tuple.FieldNumber, tuple.FieldDouble,
	tuple.FieldDecimal, tuple.FieldUUID, tuple.FieldDatetime, tuple.FieldInterval,
	tuple.FieldVarbinary, tuple.FieldArray, tuple.FieldMap,
	tuple.FieldUint8, tuple.FieldUint16, tuple.FieldUint32, tuple.FieldUint64,
	tuple.FieldInt8, tuple.FieldInt16, tuple.FieldInt32, tuple.FieldInt64,
	tuple.FieldFloat32, tuple.FieldFloat64,
	tuple.FieldDecimal32, tuple.FieldDecimal64, tuple.FieldDecimal128, tuple.FieldDecimal256,
}

// The options a round trip is checked with: the defaults, the options of
// the import of tt-ee, a null that is a word and one that is a number.
var roundTripOptions = []tuple.FieldOptions{
	{},
	{Null: "", ThousandSeparators: " `", DecimalSeparators: ".,"},
	{Null: "null"},
	{Null: "0", Unsupported: []int8{
		tuple.ExtDecimal, tuple.ExtUUID, tuple.ExtDatetime, tuple.ExtInterval,
	}},
}

// trickyValues are values whose text is easy to read back as another one.
func trickyValues(t *testing.T) []any {
	t.Helper()

	moscow, err := time.LoadLocation("Europe/Moscow")
	require.NoError(t, err)

	values := []any{
		"", "null", "0", `""`, `"\"\""`, "t", "nan", "NaN", "true", "12", "1 000", "1,5", "007",
		"-0", "+5", "[1]", "{}", `{"$uuid": "x"}`, `"q"`, " lead", "1,0,0,0,0,0,0,0,0",
		"+1 years", sampleUUID, "2024-11-14T14:02:36Z", "\xff\xfe", "é",
		uint64(0), uint64(math.MaxUint64), int64(-1), int64(math.MinInt64),
		0.0, 3.0, -0.5, 1e21, 1e-7, math.NaN(), math.Inf(1), math.Inf(-1),
		float32(0.1), float32(3), float32(math.Inf(-1)),
		true, false, tuple.Binary{}, tuple.Binary{0, 1, 0xff},
		decimal.MustNewDecimal("12.340"), decimal.MustNewDecimal("1"),
		decimal.MustNewDecimal("-0.000"),
		uuid.MustParse(sampleUUID),
		datetime.Interval{}, datetime.Interval{Nsec: -7},
		datetime.Interval{Year: 1, Month: -2, Week: 3, Day: 4, Hour: 5, Min: 6, Sec: 7, Nsec: 8,
			Adjust: datetime.ExcessAdjust},
		tuple.Ext{Type: 0x42, Data: []byte{7}}, tuple.Ext{Type: tuple.ExtError, Data: []byte{}},
		// Extensions Tarantool takes in no field: of type 0, of a negative
		// type, and of a known type with a malformed payload.
		tuple.Ext{Type: 0, Data: []byte{1}}, tuple.Ext{Type: -1, Data: []byte{1}},
		tuple.Ext{Type: tuple.ExtUUID, Data: []byte{0}},
		[]any{}, []any{nil, "1", uint64(1), 1.0, []any{tuple.Map{}}},
		tuple.Map{}, tuple.Map{{Key: "$uuid", Value: "x"}},
		tuple.Map{{Key: "b", Value: uint64(1)}, {Key: uint64(2), Value: "a"}},
		tuple.Map{{Key: "a", Value: uint64(1)}, {Key: "a", Value: uint64(2)}},
		tuple.Map{{Key: nil, Value: []any{}}, {Key: 1.5, Value: tuple.Binary("x")}},
	}

	moments := []time.Time{
		time.Date(2024, 11, 14, 14, 2, 36, 27_000_000, moscow),
		time.Date(2024, 11, 14, 14, 2, 36, 0, time.FixedZone(datetime.NoTimezone, 0)),
		time.Date(2024, 11, 14, 14, 2, 36, 5, time.FixedZone(datetime.NoTimezone, -90*60)),
		time.Date(2024, 11, 14, 14, 2, 36, 120_000, time.FixedZone("MSK", 3*3600)),
		time.Date(2024, 11, 14, 14, 2, 36, 0, time.FixedZone("A", 3600)),
		time.Date(2024, 11, 14, 14, 2, 36, 0, time.UTC),
		time.Date(-1, 1, 1, 0, 0, 0, 0, time.FixedZone(datetime.NoTimezone, 0)),
		time.Date(12345, 6, 7, 8, 9, 10, 0, time.FixedZone(datetime.NoTimezone, 14*3600)),
	}

	datetimes := make([]any, len(moments))

	for index, moment := range moments {
		datetimes[index], err = datetime.NewDatetime(moment)
		require.NoError(t, err)
	}

	return slices.Concat(values, datetimes)
}

// TestAppendFieldReadsBack writes every value in every field that holds it
// and reads it back.
func TestAppendFieldReadsBack(t *testing.T) {
	t.Parallel()

	values := append(decodeSample(t), trickyValues(t)...)

	for _, opts := range roundTripOptions {
		for _, typ := range allFieldTypes {
			for _, nullable := range []bool{false, true} {
				field := tuple.Field{Name: "f", Type: typ, IsNullable: nullable}

				for _, value := range values {
					text, err := tuple.AppendField(nil, value, field, opts)
					if err != nil {
						require.ErrorIs(t, err, tuple.ErrLossy)
						assert.False(t, holds(field, value),
							"%s field, %#v, %+v: %v", typ, value, opts, err)

						continue
					}

					assert.True(t, holds(field, value), "%s field, %#v, %+v: %q",
						typ, value, opts, text)

					back, err := tuple.ParseField(string(text), field, opts)
					require.NoError(t, err, "%s field, %#v, %+v: %q", typ, value, opts, text)
					assertSameValue(t, value, back, "%s field, %#v, %+v: %q",
						typ, value, opts, text)
				}
			}
		}
	}
}

// holds reports whether a field holds a value, by its MessagePack type, as
// Tarantool Enterprise 3.7 was seen to take it.
//
//nolint:cyclop,gocyclo // One case per type is the clearest form.
func holds(field tuple.Field, value any) bool {
	typ := field.Type

	if value == nil {
		return field.IsNullable || typ == tuple.FieldAny
	}

	switch typed := value.(type) {
	case []any:
		return typ == tuple.FieldAny || typ == tuple.FieldArray
	case tuple.Map:
		return typ == tuple.FieldAny || typ == tuple.FieldMap
	case uint64:
		limits, ok := integerLimits[typ]

		return ok && typed <= limits.high
	case int64:
		// The values hold an int64 only for a negative integer, as Decode
		// and ParseField return it.
		limits, ok := integerLimits[typ]

		return ok && typed >= limits.low
	case datetime.Interval:
		return typ == tuple.FieldAny || typ == tuple.FieldInterval
	case tuple.Ext:
		// Decode keeps a known type as an Ext only for a malformed payload.
		known := typed.Type == tuple.ExtDecimal || typed.Type == tuple.ExtUUID ||
			typed.Type == tuple.ExtDatetime || typed.Type == tuple.ExtInterval

		return typ == tuple.FieldAny && typed.Type > 0 && !known
	}

	if typ == tuple.FieldAny || typ == tuple.FieldScalar {
		return true
	}

	switch value.(type) {
	case float64:
		return typ == tuple.FieldNumber || typ == tuple.FieldDouble || typ == tuple.FieldFloat64
	case float32:
		return typ == tuple.FieldNumber || typ == tuple.FieldDouble || typ == tuple.FieldFloat32
	case decimal.Decimal:
		return typ == tuple.FieldNumber || typ == tuple.FieldDecimal ||
			typ == tuple.FieldDecimal32 || typ == tuple.FieldDecimal64 ||
			typ == tuple.FieldDecimal128 || typ == tuple.FieldDecimal256
	case string:
		return field.Type == tuple.FieldString
	case bool:
		return field.Type == tuple.FieldBoolean
	case tuple.Binary:
		return field.Type == tuple.FieldVarbinary
	case uuid.UUID:
		return field.Type == tuple.FieldUUID
	case datetime.Datetime:
		return field.Type == tuple.FieldDatetime
	}

	return false
}

// integerLimits are the ranges of integers the field types hold.
var integerLimits = map[tuple.FieldType]struct {
	low  int64
	high uint64
}{
	tuple.FieldAny:      {math.MinInt64, math.MaxUint64},
	tuple.FieldScalar:   {math.MinInt64, math.MaxUint64},
	tuple.FieldInteger:  {math.MinInt64, math.MaxUint64},
	tuple.FieldNumber:   {math.MinInt64, math.MaxUint64},
	tuple.FieldDouble:   {math.MinInt64, math.MaxUint64},
	tuple.FieldUnsigned: {0, math.MaxUint64},
	tuple.FieldUint64:   {0, math.MaxUint64},
	tuple.FieldUint32:   {0, math.MaxUint32},
	tuple.FieldUint16:   {0, math.MaxUint16},
	tuple.FieldUint8:    {0, math.MaxUint8},
	tuple.FieldInt64:    {math.MinInt64, math.MaxInt64},
	tuple.FieldInt32:    {math.MinInt32, math.MaxInt32},
	tuple.FieldInt16:    {math.MinInt16, math.MaxInt16},
	tuple.FieldInt8:     {math.MinInt8, math.MaxInt8},
}

// assertSameValue checks that two values are the same MessagePack value, as
// their tagged JSON tells.
func assertSameValue(t *testing.T, want, got any, msgAndArgs ...any) {
	t.Helper()

	wantJSON, err := tuple.AppendTaggedJSON(nil, want)
	require.NoError(t, err, msgAndArgs...)

	gotJSON, err := tuple.AppendTaggedJSON(nil, got)
	require.NoError(t, err, msgAndArgs...)

	// The text counts, not the JSON value: it keeps the order of a map and
	// tells 1 from 1.0.
	//nolint:testifylint // As above.
	assert.Equal(t, string(wantJSON), string(gotJSON), msgAndArgs...)
}

// TestAppendFieldText pins the text of values that read back as they are and
// of those that do not.
func TestAppendFieldText(t *testing.T) {
	t.Parallel()

	field := func(typ tuple.FieldType, nullable bool) tuple.Field {
		return tuple.Field{Name: "f", Type: typ, IsNullable: nullable}
	}

	dec := decimal.MustNewDecimal("12.340")

	cases := []struct {
		value any
		field tuple.Field
		want  string
	}{
		{"t", field(tuple.FieldAny, false), "t"},
		{"true", field(tuple.FieldAny, false), `"true"`},
		{"12", field(tuple.FieldScalar, false), `"12"`},
		{"", field(tuple.FieldAny, false), `""`},
		{"", field(tuple.FieldString, false), ""},
		{"", field(tuple.FieldString, true), `""`},
		{`""`, field(tuple.FieldString, true), `"\"\""`},
		{"x", field(tuple.FieldString, true), "x"},
		{nil, field(tuple.FieldString, true), ""},
		{nil, field(tuple.FieldAny, false), ""},
		{uint64(5), field(tuple.FieldAny, false), "5"},
		{3.0, field(tuple.FieldAny, false), "3.0"},
		{3.0, field(tuple.FieldDouble, false), "3.0"},
		{float32(1.5), field(tuple.FieldAny, false), `{"$float32":1.5}`},
		{float32(1.5), field(tuple.FieldFloat32, false), "1.5"},
		{math.NaN(), field(tuple.FieldNumber, false), "NaN"},
		{dec, field(tuple.FieldDecimal, false), "12.340"},
		{dec, field(tuple.FieldNumber, false), `{"$decimal":"12.340"}`},
		{dec, field(tuple.FieldAny, false), `{"$decimal":"12.340"}`},
		{tuple.Binary("ab\x00"), field(tuple.FieldVarbinary, false), "YWIA"},
		{tuple.Binary("ab\x00"), field(tuple.FieldAny, false), `{"$binary":"YWIA"}`},
		{uuid.MustParse(sampleUUID), field(tuple.FieldAny, false), sampleUUID},
		{datetime.Interval{Year: 1, Sec: -2}, field(tuple.FieldAny, false), "+1 years, -2 seconds"},
		{
			datetime.Interval{Month: 1, Adjust: datetime.LastAdjust},
			field(tuple.FieldInterval, false), "+1 months, adjust last",
		},
		{[]any{uint64(1), "a", 1.0}, field(tuple.FieldArray, false), `[1,"a",1.0]`},
		{tuple.Map{{Key: uint64(2), Value: "two"}}, field(tuple.FieldMap, false),
			`{"$map":[[2,"two"]]}`},
		{dec, field(tuple.FieldAny, false), `{"$decimal":"12.340"}`},
	}

	for _, test := range cases {
		got, err := tuple.AppendField(nil, test.value, test.field, tuple.FieldOptions{})
		require.NoError(t, err, "%#v in %+v", test.value, test.field)
		assert.Equal(t, test.want, string(got), "%#v in %+v", test.value, test.field)
	}

	// A decimal the target lacks is not guessed, so its text needs no tag.
	got, err := tuple.AppendField(nil, "2024-11-14T14:02:36Z", field(tuple.FieldAny, false),
		tuple.FieldOptions{Unsupported: []int8{tuple.ExtDatetime}})
	require.NoError(t, err)
	assert.Equal(t, "2024-11-14T14:02:36Z", string(got))

	_, err = tuple.AppendField(nil, "x", field(tuple.FieldUnsigned, false), tuple.FieldOptions{})
	require.ErrorIs(t, err, tuple.ErrLossy)

	_, err = tuple.AppendField(nil, nil, field(tuple.FieldUnsigned, false), tuple.FieldOptions{})
	require.ErrorIs(t, err, tuple.ErrLossy)
}

// TestParseFieldGuess checks the guess of the type of a value of an any
// field: strict, so that a word stays a string.
func TestParseFieldGuess(t *testing.T) {
	t.Parallel()

	anyField := tuple.Field{Name: "f", Type: tuple.FieldAny}
	scalarField := tuple.Field{Name: "f", Type: tuple.FieldScalar}
	opts := tuple.FieldOptions{ThousandSeparators: " `", DecimalSeparators: ".,"}

	moment, err := tuple.ParseDatetime("2024-11-14T14:02:36Z")
	require.NoError(t, err)

	cases := []struct {
		text  string
		field tuple.Field
		want  any
	}{
		{"", anyField, nil},
		{"t", anyField, "t"},
		{"F", anyField, "F"},
		{"nan", anyField, "nan"},
		{"inf", anyField, "inf"},
		{"007", anyField, "007"},
		{"+5", anyField, "+5"},
		{"true", anyField, true},
		{"1", anyField, uint64(1)},
		{"-1", anyField, int64(-1)},
		{"1.0", anyField, 1.0},
		{"1e3", anyField, 1000.0},
		{"1 000", anyField, uint64(1000)},
		{"1,5", anyField, 1.5},
		{"99999999999999999999", anyField, 1e20},
		{"-Infinity", anyField, math.Inf(-1)},
		{"2024-11-14T14:02:36Z", anyField, moment},
		{"2024-11-14", anyField, "2024-11-14"},
		{"2024-11-14T14:02:36", anyField, "2024-11-14T14:02:36"},
		{sampleUUID, anyField, uuid.MustParse(sampleUUID)},
		{"+1 years", anyField, datetime.Interval{Year: 1}},
		{"1 years", anyField, "1 years"},
		{"1,0,0,0,0,0,-2,0,2", anyField,
			datetime.Interval{Year: 1, Sec: -2, Adjust: datetime.LastAdjust}},
		{"[1, 2]", anyField, []any{uint64(1), uint64(2)}},
		{"[1, 2]", scalarField, "[1, 2]"},
		{`{"a": 1}`, anyField, tuple.Map{{Key: "a", Value: uint64(1)}}},
		{`{"a": 1}`, scalarField, `{"a": 1}`},
		{`{"$decimal": "1.50"}`, scalarField, decimal.MustNewDecimal("1.50")},
		{`"12"`, anyField, "12"},
		{`"12`, anyField, `"12`},
		{"[1,", anyField, "[1,"},
	}

	for _, test := range cases {
		got, err := tuple.ParseField(test.text, test.field, opts)
		require.NoError(t, err, test.text)
		assertSameValue(t, test.want, got, "%q", test.text)
	}

	unsupported := tuple.FieldOptions{
		Unsupported: []int8{tuple.ExtUUID, tuple.ExtDatetime, tuple.ExtInterval},
	}
	for _, text := range []string{sampleUUID, "2024-11-14T14:02:36Z", "+1 years"} {
		got, err := tuple.ParseField(text, anyField, unsupported)
		require.NoError(t, err)
		assert.Equal(t, text, got)
	}
}

// TestParseFieldTyped checks the texts the typed fields read, those of
// go-tupleconv among them, and those they do not.
func TestParseFieldTyped(t *testing.T) {
	t.Parallel()

	opts := tuple.FieldOptions{Null: "NULL", ThousandSeparators: " `", DecimalSeparators: ".,"}
	field := func(typ tuple.FieldType) tuple.Field { return tuple.Field{Name: "f", Type: typ} }

	cases := []struct {
		text  string
		field tuple.Field
		want  any
	}{
		{"NULL", tuple.Field{Type: tuple.FieldUnsigned, IsNullable: true}, nil},
		{"NULL", field(tuple.FieldString), "NULL"},
		{`"NULL"`, tuple.Field{Type: tuple.FieldString, IsNullable: true}, "NULL"},
		{`"x"`, tuple.Field{Type: tuple.FieldString, IsNullable: true}, `"x"`},
		{"1 000", field(tuple.FieldUnsigned), uint64(1000)},
		{"+5", field(tuple.FieldInteger), uint64(5)},
		{"-0", field(tuple.FieldInteger), uint64(0)},
		{"-5", field(tuple.FieldInteger), int64(-5)},
		{"255", field(tuple.FieldUint8), uint64(255)},
		{"-128", field(tuple.FieldInt8), int64(-128)},
		// A double field holds an integer as Tarantool stores it.
		{"3", field(tuple.FieldDouble), uint64(3)},
		{"3.0", field(tuple.FieldDouble), 3.0},
		{"3", field(tuple.FieldFloat64), 3.0},
		{"1.255", field(tuple.FieldDecimal64), decimal.MustNewDecimal("1.255")},
		// A scalar takes no interval, and nil only when it is nullable.
		{"+1 years", field(tuple.FieldScalar), "+1 years"},
		{"NULL", field(tuple.FieldScalar), "NULL"},
		{"NULL", tuple.Field{Type: tuple.FieldScalar, IsNullable: true}, nil},
		{"1,5", field(tuple.FieldDouble), 1.5},
		{"inf", field(tuple.FieldDouble), math.Inf(1)},
		{"-Infinity", field(tuple.FieldFloat64), math.Inf(-1)},
		{"0e-400", field(tuple.FieldDouble), 0.0},
		{"0.1", field(tuple.FieldFloat32), float32(0.1)},
		{"3", field(tuple.FieldNumber), uint64(3)},
		{"3.5", field(tuple.FieldNumber), 3.5},
		{`{"$decimal": "1.50"}`, field(tuple.FieldNumber), decimal.MustNewDecimal("1.50")},
		{"1.50", field(tuple.FieldDecimal), decimal.MustNewDecimal("1.50")},
		{"t", field(tuple.FieldBoolean), true},
		{"YWIA", field(tuple.FieldVarbinary), tuple.Binary("ab\x00")},
		{"1,0,0,0,0,0,-2,0,2", field(tuple.FieldInterval),
			datetime.Interval{Year: 1, Sec: -2, Adjust: datetime.LastAdjust}},
		{"+1 year, -2 second", field(tuple.FieldInterval), datetime.Interval{Year: 1, Sec: -2}},
		{"[]", field(tuple.FieldArray), []any{}},
		{"{}", field(tuple.FieldMap), tuple.Map{}},
	}

	for _, test := range cases {
		got, err := tuple.ParseField(test.text, test.field, opts)
		require.NoError(t, err, test.text)
		assertSameValue(t, test.want, got, "%q", test.text)
	}

	for _, test := range []struct {
		text  string
		field tuple.Field
	}{
		{"NULL", field(tuple.FieldUnsigned)},
		{"256", field(tuple.FieldUint8)},
		{"-1", field(tuple.FieldUnsigned)},
		{"-129", field(tuple.FieldInt8)},
		{"x", field(tuple.FieldNumber)},
		// strconv takes these, Tarantool and other tools write none of them.
		{"1_000", field(tuple.FieldDouble)},
		{"0x1p-2", field(tuple.FieldFloat64)},
		{"infinite", field(tuple.FieldDouble)},
		// Too small for the float, where 0 would be read.
		{"1e-400", field(tuple.FieldDouble)},
		{"1e-50", field(tuple.FieldFloat32)},
		{`{"$uuid": "` + sampleUUID + `"}`, field(tuple.FieldNumber)},
		{"YWI", field(tuple.FieldVarbinary)},
		{"[]", field(tuple.FieldMap)},
		{"{}", field(tuple.FieldArray)},
		{"1", field("num")},
	} {
		_, err := tuple.ParseField(test.text, test.field, opts)
		require.Error(t, err, "%q in a %s field", test.text, test.field.Type)
	}

	_, err := tuple.ParseField("1", field("num"), opts)
	require.ErrorIs(t, err, tuple.ErrFieldType)

	_, err = tuple.AppendField(nil, uint64(1), field("num"), opts)
	require.ErrorIs(t, err, tuple.ErrFieldType)

	_, err = tuple.ParseField("x", field(tuple.FieldNumber), opts)
	require.ErrorIs(t, err, tuple.ErrFieldValue)
}
