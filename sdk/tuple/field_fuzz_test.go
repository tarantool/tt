package tuple_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"

	"github.com/tarantool/tt/sdk/tuple"
)

// unknownFieldType is a field type the package does not know.
const unknownFieldType tuple.FieldType = "num"

// fieldFuzzOptions are the options FuzzAppendField picks from: those of the
// round trip tests, separators that make a point a thousand separator, the
// extension types a target may lack, and options that are not valid.
var fieldFuzzOptions = []tuple.FieldOptions{
	{},
	{Null: "", ThousandSeparators: " `", DecimalSeparators: ".,"},
	{Null: "null"},
	{Null: "0", Unsupported: []int8{
		tuple.ExtDecimal, tuple.ExtUUID, tuple.ExtDatetime, tuple.ExtInterval,
	}},
	{ThousandSeparators: ".", DecimalSeparators: ","},
	{ThousandSeparators: "'", DecimalSeparators: ","},
	{ThousandSeparators: "_ ", DecimalSeparators: "·"},
	{Unsupported: []int8{tuple.ExtDatetime}},
	{Null: "NULL", Unsupported: []int8{tuple.ExtUUID, tuple.ExtInterval}},
	{ThousandSeparators: " ", DecimalSeparators: ",", Null: `""`},
	{Null: "true"},
	{Null: "[]", ThousandSeparators: ",", DecimalSeparators: "."},
	{ThousandSeparators: "."},
	{ThousandSeparators: ". ", Null: "0.0"},
	{ThousandSeparators: "0"},
	{DecimalSeparators: "-"},
	{ThousandSeparators: ",", DecimalSeparators: ","},
	{Null: "\xff"},
}

// The flags of the field and the options FuzzAppendField and
// FuzzParseFieldOptions build.
const (
	flagNullable = 1 << iota
	flagDefault
	flagDefaultFunc
	flagRawVarbinary
	flagLoose
	flagNull
)

// fuzzField is the field of a choice of type and of flags: a type of the
// package or one it does not know, nullable, with a default.
func fuzzField(choice, flags uint8) tuple.Field {
	types := append(slices.Clone(allFieldTypes), unknownFieldType)
	field := tuple.Field{
		Name: "f", Type: types[int(choice)%len(types)], IsNullable: flags&flagNullable != 0,
		Default: nil, DefaultFunc: "",
	}

	if flags&flagDefault != 0 {
		field.Default = uint64(0)
	}

	if flags&flagDefaultFunc != 0 {
		field.DefaultFunc = "next_id"
	}

	return field
}

// fuzzOptions are options of the table with the flags applied.
func fuzzOptions(base tuple.FieldOptions, flags uint8, null string) tuple.FieldOptions {
	opts := base

	opts.Unsupported = slices.Clone(base.Unsupported)
	opts.RawVarbinary = flags&flagRawVarbinary != 0
	opts.Loose = flags&flagLoose != 0

	if flags&flagNull != 0 {
		opts.Null = null
	}

	return opts
}

// knownFieldType reports whether the package knows a field type.
func knownFieldType(typ tuple.FieldType) bool {
	return slices.Contains(allFieldTypes, typ)
}

// looseAny is the field loose options write a value its field does not hold
// in.
var looseAny = tuple.Field{Name: "f", Type: tuple.FieldAny, IsNullable: true}

// FuzzAppendField checks that AppendField writes a value exactly when its
// field holds it, with a text that reads back as the value and is written
// the same again, leaves dst as it is on an error, and, with loose options,
// writes a value its field does not hold as one of a nullable field of type
// any.
func FuzzAppendField(f *testing.F) {
	limitMemory(f)

	for index, seed := range generatorSeeds() {
		choice := uint8(index)
		f.Add(seed, choice, choice/3, choice/7, "")
		f.Add(seed, choice*7, uint8(flagLoose|flagRawVarbinary), choice, "x")
	}

	f.Fuzz(func(t *testing.T, data []byte, typ, flags, choice uint8, null string) {
		value := genValue(t, data)
		skipKnownBugs(t, value)

		field := fuzzField(typ, flags)
		opts := fuzzOptions(fieldFuzzOptions[int(choice)%len(fieldFuzzOptions)], flags, null)
		msg := fmt.Sprintf("%s in %+v with %+v", describe(value), field, opts)

		const head, room = "cell:", 64

		buffer := bytes.Repeat([]byte{0xaa}, room)
		prefix := buffer[:len(head)]
		copy(prefix, head)

		out, err := tuple.AppendField(prefix, value, field, opts)
		if err != nil {
			require.Equal(t, head, string(out), msg)
			require.Equal(t, bytes.Repeat([]byte{0xaa}, room-len(head)), buffer[len(head):],
				"%s: dst is written on an error", msg)
		} else {
			require.Equal(t, head, string(out[:len(head)]), msg)
		}

		if opts.Validate() != nil {
			require.ErrorIs(t, err, tuple.ErrFieldOptions, msg)

			return
		}

		strict := opts

		strict.Loose = false

		if opts.Loose {
			checkLooseField(t, value, field, strict, out[min(len(head), len(out)):], err, msg)

			return
		}

		switch {
		case !knownFieldType(field.Type):
			require.ErrorIs(t, err, tuple.ErrFieldType, msg)
		case !fieldHolds(field, value):
			require.ErrorIs(t, err, tuple.ErrLossy, msg)
		default:
			require.NoError(t, err, msg)
			checkFieldText(t, value, field, opts, string(out[len(head):]), msg)
		}
	})
}

// checkFieldText checks that the text AppendField wrote for a value reads
// back as it, and that AppendField writes it again the same.
func checkFieldText(t *testing.T, value any, field tuple.Field, opts tuple.FieldOptions,
	text, msg string,
) {
	t.Helper()

	back, err := tuple.ParseField(text, field, opts)
	require.NoError(t, err, "%s: %q", msg, text)
	requireSame(t, value, back, "%s: %q", msg, text)

	again, err := tuple.AppendField(nil, value, field, opts)
	require.NoError(t, err)
	require.Equal(t, text, string(again), "%s: AppendField is not deterministic", msg)

	written, err := tuple.AppendField(nil, back, field, opts)
	require.NoError(t, err)
	require.Equal(t, text, string(written), "%s: the text is not a fixed point", msg)
}

// checkLooseField checks what AppendField wrote with loose options: what
// strict options write for a value the field holds, for a string in a
// varbinary field that of its bytes, and for any other value the text of a
// nullable field of type any.
func checkLooseField(t *testing.T, value any, field tuple.Field, strict tuple.FieldOptions,
	text []byte, err error, msg string,
) {
	t.Helper()

	held := knownFieldType(field.Type) && fieldHolds(field, value)

	var (
		want    []byte
		wantErr error
		reads   = field
		expect  = value
		textual = string(text)
	)

	str, isString := value.(string)

	switch {
	case held:
		want, wantErr = tuple.AppendField(nil, value, field, strict)
	case field.Type == tuple.FieldVarbinary && isString:
		want, wantErr = tuple.AppendField(nil, tuple.Binary(str), field, strict)
		expect = tuple.Binary(str)
	default:
		wantErr = errors.New("no text")
	}

	if wantErr != nil {
		want, wantErr = tuple.AppendField(nil, value, looseAny, strict)
		reads, expect = looseAny, value
	}

	require.Equal(t, wantErr == nil, err == nil, "%s: %v, %v", msg, err, wantErr)

	if err != nil {
		require.ErrorIs(t, err, tuple.ErrLossy, msg)

		return
	}

	require.Equal(t, string(want), textual, msg)

	back, err := tuple.ParseField(textual, reads, strict)
	require.NoError(t, err, "%s: %q", msg, text)
	requireSame(t, expect, back, "%s: %q", msg, text)
}

// separatorCandidates are the characters FuzzParseFieldOptions takes its
// separators from, by the bits of a mask: those of the locales, and some a
// separator cannot be.
var separatorCandidates = []rune{
	' ', '`', '\'', '.', ',', '_', ' ', ' ', '·', ':', ';', '$', 'e', '-', '"', '\t',
}

// separatorsOf are the candidates of the bits of mask.
func separatorsOf(mask uint16) string {
	var out strings.Builder

	for index, char := range separatorCandidates {
		if mask&(1<<index) != 0 {
			out.WriteRune(char)
		}
	}

	return out.String()
}

// unsupportedOf are the extension types of the bits of mask.
func unsupportedOf(mask uint8) []int8 {
	var out []int8

	for index, typ := range []int8{
		tuple.ExtDecimal, tuple.ExtUUID, tuple.ExtDatetime, tuple.ExtInterval, 0x42,
	} {
		if mask&(1<<index) != 0 {
			out = append(out, typ)
		}
	}

	return out
}

// typedFieldOf is the field type of a value of the model of Decode that is
// not a string, nil or an Ext.
func typedFieldOf(value any) (tuple.FieldType, bool) {
	switch value.(type) {
	case uint64, int64:
		return tuple.FieldInteger, true
	case float64:
		return tuple.FieldDouble, true
	case float32:
		return tuple.FieldFloat32, true
	case decimal.Decimal:
		return tuple.FieldDecimal, true
	case bool:
		return tuple.FieldBoolean, true
	case uuid.UUID:
		return tuple.FieldUUID, true
	case datetime.Datetime:
		return tuple.FieldDatetime, true
	case datetime.Interval:
		return tuple.FieldInterval, true
	case tuple.Binary:
		return tuple.FieldVarbinary, true
	case []any:
		return tuple.FieldArray, true
	case tuple.Map:
		return tuple.FieldMap, true
	}

	return "", false
}

// fitsScalar reports whether a field of type scalar holds a value.
func fitsScalar(value any) bool {
	switch value.(type) {
	case []any, tuple.Map, datetime.Interval, tuple.Ext:
		return false
	}

	return true
}

// FuzzParseFieldOptions checks ParseField on any text, field and options:
// it reports only its errors, reads a value the field holds that is written
// back as it, guesses the same in a scalar and an any field, and guesses
// what the parser of the type it guesses reads.
func FuzzParseFieldOptions(f *testing.F) {
	limitMemory(f)

	for index, seed := range append(slices.Clone(trickyStrings), datetimeTexts...) {
		choice := uint8(index)
		f.Add(seed, choice, choice/5, "", uint16(0), uint16(0), uint8(0))
		f.Add(seed, uint8(0), uint8(flagNullable|flagNull), "null", uint16(1), uint16(0x18),
			choice)
	}

	f.Add("1.234,5", uint8(0), uint8(0), "", uint16(8), uint16(16), uint8(0))
	f.Add("+Inf", uint8(1), uint8(0), "", uint16(0), uint16(0), uint8(0))
	f.Add(`"YWJj"`, uint8(12), uint8(0), "", uint16(0), uint16(0), uint8(0))
	f.Add("abc", uint8(12), uint8(flagRawVarbinary), "", uint16(0), uint16(0), uint8(0))
	// The choice of the type after those of the package is one it does not know.
	f.Add("1", uint8(29), uint8(flagLoose), "", uint16(0), uint16(0), uint8(0))

	f.Fuzz(func(t *testing.T, text string, typ, flags uint8, null string, thousands,
		decimals uint16, unsupported uint8,
	) {
		field := fuzzField(typ, flags)
		opts := fuzzOptions(tuple.FieldOptions{
			Null: "", ThousandSeparators: separatorsOf(thousands),
			DecimalSeparators: separatorsOf(decimals), Unsupported: unsupportedOf(unsupported),
			RawVarbinary: false, Loose: false,
		}, flags, null)
		msg := fmt.Sprintf("%q in %+v with %+v", text, field, opts)

		value, err := tuple.ParseField(text, field, opts)

		switch {
		case opts.Validate() != nil:
			require.ErrorIs(t, err, tuple.ErrFieldOptions, msg)

			return
		case !knownFieldType(field.Type) && !opts.Loose:
			require.ErrorIs(t, err, tuple.ErrFieldType, msg)

			return
		case err != nil:
			require.ErrorIs(t, err, tuple.ErrFieldValue, msg)

			return
		}

		reads := field
		if !knownFieldType(field.Type) {
			reads = looseAny
		}

		require.True(t, fieldHolds(reads, value), "%s: %s", msg, describe(value))

		if knownBug(value) == "" {
			written, err := tuple.AppendField(nil, value, field, opts)
			require.NoError(t, err, "%s: %s", msg, describe(value))
			checkFieldText(t, value, field, opts, string(written), msg)
		}

		if reads.Type == tuple.FieldAny || reads.Type == tuple.FieldScalar {
			checkGuess(t, text, reads, opts, value, msg)
		}
	})
}

// checkGuess checks the guess of the type of the value of an any or scalar
// field: an extension type the options leave out is read only from tagged
// JSON, a scalar field reads what an any field does if it holds it and the
// text as a string otherwise, and the parser of the type guessed reads the
// same value.
func checkGuess(t *testing.T, text string, field tuple.Field, opts tuple.FieldOptions,
	value any, msg string,
) {
	t.Helper()

	// A quoted text of nil is read as the text in the quotes.
	inner := text

	var unquoted string

	if json.Unmarshal([]byte(text), &unquoted) == nil {
		inner = unquoted
	}

	if typ, ok := extTypeOf(value); ok && slices.Contains(opts.Unsupported, typ) {
		require.True(t, strings.HasPrefix(strings.TrimSpace(inner), "{") ||
			strings.HasPrefix(text, "{"), "%s: an unsupported %s is guessed", msg, describe(value))
	}

	nullable := field

	nullable.IsNullable = true

	anyField, scalarField := nullable, nullable

	anyField.Type, scalarField.Type = tuple.FieldAny, tuple.FieldScalar

	anyValue, err := tuple.ParseField(text, anyField, opts)
	require.NoError(t, err, msg)

	scalarValue, err := tuple.ParseField(text, scalarField, opts)
	require.NoError(t, err, msg)

	if fitsScalar(anyValue) {
		requireSame(t, anyValue, scalarValue, "%s: scalar and any differ", msg)
	} else {
		_, isString := scalarValue.(string)
		require.True(t, isString, "%s: a scalar field reads %s", msg, describe(scalarValue))
	}

	typ, ok := typedFieldOf(anyValue)
	if !ok || (typ == tuple.FieldVarbinary && opts.RawVarbinary) {
		return
	}

	typed := nullable

	typed.Type = typ

	back, err := tuple.ParseField(text, typed, opts)
	require.NoError(t, err, "%s: guessed %s, the %s field does not read it", msg,
		describe(anyValue), typ)
	requireSame(t, anyValue, back, "%s: guessed and read as %s differ", msg, typ)
}

// extTypeOf is the extension type of a value of a type of the extensions
// Tarantool defines.
func extTypeOf(value any) (int8, bool) {
	switch value.(type) {
	case decimal.Decimal:
		return tuple.ExtDecimal, true
	case uuid.UUID:
		return tuple.ExtUUID, true
	case datetime.Datetime:
		return tuple.ExtDatetime, true
	case datetime.Interval:
		return tuple.ExtInterval, true
	}

	return 0, false
}
