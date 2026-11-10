package tuple

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
)

// FieldType is the type of a field in the format of a space, as
// box.space.<name>:format() names it.
type FieldType string

// The field types of Tarantool.
const (
	FieldAny       FieldType = "any"
	FieldScalar    FieldType = "scalar"
	FieldString    FieldType = "string"
	FieldBoolean   FieldType = "boolean"
	FieldUnsigned  FieldType = "unsigned"
	FieldInteger   FieldType = "integer"
	FieldNumber    FieldType = "number"
	FieldDouble    FieldType = "double"
	FieldDecimal   FieldType = "decimal"
	FieldUUID      FieldType = "uuid"
	FieldDatetime  FieldType = "datetime"
	FieldInterval  FieldType = "interval"
	FieldVarbinary FieldType = "varbinary"
	FieldArray     FieldType = "array"
	FieldMap       FieldType = "map"
	FieldUint8     FieldType = "uint8"
	FieldUint16    FieldType = "uint16"
	FieldUint32    FieldType = "uint32"
	FieldUint64    FieldType = "uint64"
	FieldInt8      FieldType = "int8"
	FieldInt16     FieldType = "int16"
	FieldInt32     FieldType = "int32"
	FieldInt64     FieldType = "int64"
	FieldFloat32   FieldType = "float32"
	FieldFloat64   FieldType = "float64"
	// FieldDecimal32 and the other decimals of a fixed size of Tarantool
	// Enterprise have a scale as well: Tarantool checks that a value fits.
	FieldDecimal32  FieldType = "decimal32"
	FieldDecimal64  FieldType = "decimal64"
	FieldDecimal128 FieldType = "decimal128"
	FieldDecimal256 FieldType = "decimal256"
)

// fieldTypes are the field types this package knows.
var fieldTypes = map[FieldType]bool{
	FieldAny: true, FieldScalar: true, FieldString: true, FieldBoolean: true,
	FieldUnsigned: true, FieldInteger: true, FieldNumber: true, FieldDouble: true,
	FieldDecimal: true, FieldUUID: true, FieldDatetime: true, FieldInterval: true,
	FieldVarbinary: true, FieldArray: true, FieldMap: true,
	FieldUint8: true, FieldUint16: true, FieldUint32: true, FieldUint64: true,
	FieldInt8: true, FieldInt16: true, FieldInt32: true, FieldInt64: true,
	FieldFloat32: true, FieldFloat64: true,
	FieldDecimal32: true, FieldDecimal64: true, FieldDecimal128: true, FieldDecimal256: true,
}

// isDecimal reports whether typ is decimal or a decimal of a fixed size.
func isDecimal(typ FieldType) bool {
	return typ == FieldDecimal || typ == FieldDecimal32 || typ == FieldDecimal64 ||
		typ == FieldDecimal128 || typ == FieldDecimal256
}

// Field is a field in the format of a space, as box.space.<name>:format()
// and the metadata of crud give it.
type Field struct {
	Name       string    `msgpack:"name"`
	Type       FieldType `msgpack:"type"`
	IsNullable bool      `msgpack:"is_nullable"`
	// Default and DefaultFunc are the default of the field Tarantool 3 puts
	// in place of nil, and the function that makes it.
	Default     any    `msgpack:"default"`
	DefaultFunc string `msgpack:"default_func"`
}

// nullable reports whether the field takes nil: a field marked so, a field
// of type any, which takes nil anyway, and a field with a default, which
// Tarantool puts in place of nil.
func (f Field) nullable() bool {
	return f.IsNullable || f.Type == FieldAny || f.Default != nil || f.DefaultFunc != ""
}

// FieldOptions are how the text of a field reads.
type FieldOptions struct {
	// Null is the text of nil in a field that takes nil: an empty text, if
	// left so.
	Null string
	// ThousandSeparators are the characters a number may have between its
	// digits, which reading it drops.
	ThousandSeparators string
	// DecimalSeparators are the characters, besides the point, that may
	// separate the fraction of a number.
	DecimalSeparators string
	// Unsupported are the extension types - [ExtDecimal], [ExtUUID],
	// [ExtDatetime], [ExtInterval] - the target Tarantool lacks, which the
	// type of a value of an any or scalar field is never guessed as.
	Unsupported []int8
	// RawVarbinary makes the text of a varbinary field its bytes as they
	// are rather than their base64.
	RawVarbinary bool
	// Loose makes AppendField write a value its field does not hold, or one
	// of a field type this package does not know, as it writes a value of a
	// nullable field of type any, and a string in a varbinary field as its
	// bytes, rather than fail; ParseField reads a field type it does not know
	// as any. A value that went through Lua may come with another type than
	// its field: a varbinary as a string in Tarantool 2.x.
	Loose bool
}

var (
	// ErrFieldValue reports a text that is not a value of the type of a
	// field.
	ErrFieldValue = errors.New("not a value of the field")
	// ErrFieldType reports a field type this package does not know.
	ErrFieldType = errors.New("unknown field type")
	// ErrLossy reports a value AppendField has no text for that reads back as
	// the same value: one of a type the field does not hold, such as a string
	// in an unsigned field, or nil in a field that does not take nil.
	ErrLossy = errors.New("no text reads back as the value")
	// ErrFieldOptions reports options a text could not be read with: a
	// separator that is part of a number or starts tagged JSON, one both a
	// thousand and a decimal separator, a text of nil that is not UTF-8.
	ErrFieldOptions = errors.New("invalid field options")
)

// forbiddenSeparators are the characters of the texts of numbers and of
// tagged JSON, which a separator dropped or replaced in a number would
// change.
const forbiddenSeparators = "+-\"{["

// Validate reports options a text could not be read with, as an error that
// wraps [ErrFieldOptions]. ParseField and AppendField check their options
// with it.
func (o FieldOptions) Validate() error {
	if !utf8.ValidString(o.Null) {
		return fmt.Errorf("%w: the text of nil %q is not UTF-8", ErrFieldOptions, o.Null)
	}

	for _, separators := range []string{o.ThousandSeparators, o.DecimalSeparators} {
		for _, char := range separators {
			if char == utf8.RuneError || unicode.IsLetter(char) || unicode.IsDigit(char) ||
				unicode.IsControl(char) || strings.ContainsRune(forbiddenSeparators, char) {
				return fmt.Errorf("%w: %q cannot be a separator", ErrFieldOptions, char)
			}

			if strings.ContainsRune(o.ThousandSeparators, char) &&
				strings.ContainsRune(o.DecimalSeparators, char) {
				return fmt.Errorf("%w: %q is a thousand and a decimal separator",
					ErrFieldOptions, char)
			}
		}
	}

	return nil
}

// ParseField parses text as a value of field: the text of nil in a field
// that takes nil, or the text of the type of the field. In a field that takes
// nil, the text of nil put in the double quotes of JSON once or more is the
// value of that text put in quotes one time fewer: with an empty text of nil,
// "" in a string field is the empty string and "\"\"" the string "", and with
// 0 for nil, "0" in an unsigned field is 0.
//
//   - A string field takes text as it is.
//   - A boolean is true or false, or another spelling strconv.ParseBool
//     takes. An integer, a float and a decimal are in decimal, with the
//     thousand and decimal separators of opts; an integer of the field types
//     of a fixed size must fit it. A float may be NaN, Infinity or -Infinity
//     as well. A number and a double field take an integer as an integer,
//     as Tarantool stores it, and any other number as a float64; a float32
//     and a float64 field take a float of their size.
//   - A varbinary is base64, a UUID its text, a datetime and an interval as
//     [ParseDatetime] and [ParseInterval] read them, an array and a map
//     tagged JSON, as [ParseTaggedJSON] reads it.
//   - The type of a value of an any or scalar field is guessed: an integer
//     or a float written as JSON writes them, NaN, Infinity or -Infinity,
//     true or false, a datetime with a time zone, a UUID in its canonical
//     form, in an any field an interval that starts with a sign or in the
//     text go-tupleconv writes, then tagged JSON - an array, an object, a
//     string in double quotes - and any other text is a string. A datetime,
//     a UUID and an interval are guessed only if the extension type is not
//     in opts.Unsupported, and a scalar field, like Tarantool, takes no
//     array, map, interval or other extension.
//
// A field of any type but string takes as well the tagged JSON of a value of
// its type, such as {"$decimal": "1.50"} in a number field.
//
// The values are those [Decode] returns: an integer is a uint64, or an int64
// when it is negative. A field type this package does not know is an error
// that wraps [ErrFieldType], unless opts are loose, invalid options one that
// wraps [ErrFieldOptions].
func ParseField(text string, field Field, opts FieldOptions) (any, error) {
	err := opts.Validate()
	if err != nil {
		return nil, err
	}

	if !fieldTypes[field.Type] {
		if !opts.Loose {
			return nil, fmt.Errorf("%w %q", ErrFieldType, field.Type)
		}

		field = looseField(field)
	}

	if field.nullable() {
		if text == opts.Null {
			return nil, nil //nolint:nilnil // nil is the value.
		}

		if isQuotedNull(text, opts.Null) {
			return parseValue(unquote(text), field, opts)
		}
	}

	return parseValue(text, field, opts)
}

// parseValue parses text as a value of the type of field.
func parseValue(text string, field Field, opts FieldOptions) (any, error) {
	switch field.Type { //nolint:exhaustive // parseTyped parses the other types.
	case FieldString:
		return text, nil
	case FieldAny, FieldScalar:
		return guessValue(text, field, opts), nil
	}

	value, err := parseTyped(text, field.Type, opts)
	if err == nil {
		return value, nil
	}

	if strings.HasPrefix(text, "{") {
		if tagged, ok := taggedOf(text, field.Type); ok {
			return tagged, nil
		}
	}

	return nil, fmt.Errorf("%q is not a value of a %s field: %w", text, field.Type, ErrFieldValue)
}

// taggedOf parses text as the tagged JSON of a value of a field of type typ.
func taggedOf(text string, typ FieldType) (any, bool) {
	value, err := ParseTaggedJSON([]byte(text))

	return value, err == nil && fits(value, typ)
}

// AppendField appends the text of value in field, which [ParseField] with
// the same options reads back as the same value: nil is the text of nil in a
// field that takes nil, a string in a string field is itself, and any other
// value its text in the type of the field if that reads back as it, its
// tagged JSON otherwise. A text that is the text of nil put in quotes zero
// times or more is put in quotes once more. So a value of a field of type
// any is its text if the guess of its type is right, and a string such as
// "12" or "true" there is "\"12\"" or "\"true\"".
//
// A number is written with the separators of opts where a point would be a
// thousand separator: 1,5 with a point for thousands and a comma for
// decimals.
//
// A value with no text that reads back as it is an error that wraps
// [ErrLossy], a field type this package does not know one that wraps
// [ErrFieldType] - unless opts are loose, which write either as a value of
// a nullable field of type any - and invalid options one that wraps
// [ErrFieldOptions]. dst is returned as it is on an error.
func AppendField(dst []byte, value any, field Field, opts FieldOptions) ([]byte, error) {
	err := opts.Validate()
	if err != nil {
		return dst, err
	}

	if !fieldTypes[field.Type] {
		if !opts.Loose {
			return dst, fmt.Errorf("%w %q", ErrFieldType, field.Type)
		}

		return appendField(dst, value, looseField(field), opts)
	}

	out, err := appendField(dst, value, field, opts)
	if err == nil || !opts.Loose {
		return out, err
	}

	if text, ok := value.(string); ok && field.Type == FieldVarbinary {
		out, err = appendField(dst, Binary(text), field, opts)
		if err == nil {
			return out, nil
		}
	}

	return appendField(dst, value, looseField(field), opts)
}

// looseField is the field loose options write and read a value its field
// does not hold, or of a field type this package does not know, as.
func looseField(field Field) Field {
	return Field{Name: field.Name, Type: FieldAny, IsNullable: true, Default: nil, DefaultFunc: ""}
}

// appendField appends the text of value in field, of a type this package
// knows.
func appendField(dst []byte, value any, field Field, opts FieldOptions) ([]byte, error) {
	if isNil(value) {
		if !field.nullable() {
			return dst, fmt.Errorf("nil in the %s field %q, which does not take nil: %w",
				field.Type, field.Name, ErrLossy)
		}

		return append(dst, opts.Null...), nil
	}

	if text, ok := value.(string); ok && field.Type == FieldString {
		// A string is itself unless it is put in quotes, which a string of
		// text that is not UTF-8 would not read back from.
		escaped := escapeNull(text, field, opts)
		if escaped == text || readsBack(escaped, value, field, opts) {
			return append(dst, escaped...), nil
		}
	}

	if plain, ok := plainText(value, field.Type, opts); ok {
		if text := escapeNull(plain, field, opts); readsBack(text, value, field, opts) {
			return append(dst, text...), nil
		}
	}

	tagged, err := AppendTaggedJSON(nil, value)
	if err == nil {
		if text := escapeNull(string(tagged), field, opts); readsBack(text, value, field, opts) {
			return append(dst, text...), nil
		}
	}

	return dst, fmt.Errorf("%T in the %s field %q: %w", value, field.Type, field.Name, ErrLossy)
}

// escapeNull puts text in quotes once more if it is the text of nil put in
// quotes zero times or more, in a field that takes nil.
func escapeNull(text string, field Field, opts FieldOptions) string {
	if field.nullable() && (text == opts.Null || isQuotedNull(text, opts.Null)) {
		return string(appendJSONString(nil, text))
	}

	return text
}

// isNil reports whether value is nil or a nil pointer.
func isNil(value any) bool {
	if value == nil {
		return true
	}

	switch typed := value.(type) {
	case *decimal.Decimal:
		return typed == nil
	case *datetime.Datetime:
		return typed == nil
	case *datetime.Interval:
		return typed == nil
	}

	return false
}

// plainText is the text of a value before AppendField checks it: a float
// with a point, so that it does not read back as an integer, the tagged JSON
// of an array or a map, the Text of any other value - a number with the
// decimal separator of opts where a point would be a thousand separator.
func plainText(value any, typ FieldType, opts FieldOptions) (string, bool) {
	switch typed := value.(type) {
	case float64:
		if !math.IsNaN(typed) && !math.IsInf(typed, 0) {
			return localNumber(floatLiteral(typed, float64Bits), opts), true
		}
	case float32:
		if number := float64(typed); !math.IsNaN(number) && !math.IsInf(number, 0) {
			return localNumber(floatLiteral(number, float32Bits), opts), true
		}
	case decimal.Decimal:
		return localNumber(decimalText(typed), opts), true
	case Binary:
		if typ == FieldVarbinary && opts.RawVarbinary {
			return string(typed), true
		}
	case []byte:
		if typ == FieldVarbinary && opts.RawVarbinary {
			return string(typed), true
		}
	}

	if text, ok := Text(value); ok && typ != FieldArray && typ != FieldMap {
		return text, true
	}

	tagged, err := AppendTaggedJSON(nil, value)

	return string(tagged), err == nil
}

// localNumber is the text of a number that reads back with opts: if the
// point is a thousand separator there, with the point replaced with the
// first decimal separator, or, with none, as digits and an exponent: 15e-1.
func localNumber(text string, opts FieldOptions) string {
	if !strings.ContainsRune(opts.ThousandSeparators, '.') || !strings.Contains(text, ".") {
		return text
	}

	if opts.DecimalSeparators != "" {
		separator, _ := utf8.DecodeRuneInString(opts.DecimalSeparators)

		return strings.Replace(text, ".", string(separator), 1)
	}

	return withoutPoint(text)
}

// withoutPoint is the text of a number, d.ddd with an exponent or none, as
// its digits and an exponent: 1.50 is 150e-2, 1.5e+21 is 15e20 and
// 0.000001 is 1e-6. A decimal keeps its scale: it has an exponent of the
// digits it had after the point.
func withoutPoint(text string) string {
	mantissa, exponentText, hasExponent := strings.Cut(strings.ReplaceAll(text, "E", "e"), "e")

	exponent := 0
	if hasExponent {
		exponent, _ = strconv.Atoi(exponentText)
	}

	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}

	whole, fraction, _ := strings.Cut(mantissa, ".")

	exponent -= len(fraction)

	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		digits = "0"
	}

	return sign + digits + "e" + strconv.Itoa(exponent)
}

// readsBack reports whether text in field reads back as value.
func readsBack(text string, value any, field Field, opts FieldOptions) bool {
	back, err := ParseField(text, field, opts)

	return err == nil && sameValue(back, value)
}

// sameValue reports whether two values are the same MessagePack value: they
// have the same tagged JSON, which keeps every difference between them.
func sameValue(left, right any) bool {
	leftJSON, err := AppendTaggedJSON(nil, left)
	if err != nil {
		return false
	}

	rightJSON, err := AppendTaggedJSON(nil, right)

	return err == nil && bytes.Equal(leftJSON, rightJSON)
}

// isQuotedNull reports whether text is null put in double quotes of JSON one
// time or more.
func isQuotedNull(text, null string) bool {
	for {
		inner, ok := unquoteJSON(text)
		if !ok {
			return false
		}

		if inner == null {
			return true
		}

		text = inner
	}
}

// unquote takes text, a JSON string, out of its quotes.
func unquote(text string) string {
	inner, _ := unquoteJSON(text)

	return inner
}

// unquoteJSON takes text out of its quotes if it is a JSON string of valid
// UTF-8, which, unlike a string of other bytes, reads back as itself.
func unquoteJSON(text string) (string, bool) {
	if len(text) < len(`""`) || text[0] != '"' || text[len(text)-1] != '"' ||
		!utf8.ValidString(text) {
		return "", false
	}

	var inner string

	return inner, json.Unmarshal([]byte(text), &inner) == nil
}

// The ranges of the integer field types.
var (
	unsignedLimits = map[FieldType]uint64{
		FieldUnsigned: math.MaxUint64, FieldUint64: math.MaxUint64,
		FieldUint32: math.MaxUint32, FieldUint16: math.MaxUint16, FieldUint8: math.MaxUint8,
	}
	signedLimits = map[FieldType][2]int64{
		FieldInt64: {math.MinInt64, math.MaxInt64},
		FieldInt32: {math.MinInt32, math.MaxInt32},
		FieldInt16: {math.MinInt16, math.MaxInt16},
		FieldInt8:  {math.MinInt8, math.MaxInt8},
	}
)

// parseTyped parses text as a value of a type that is not any, scalar or
// string.
//
//nolint:cyclop // One case per type is the clearest form.
func parseTyped(text string, typ FieldType, opts FieldOptions) (any, error) {
	if _, ok := unsignedLimits[typ]; ok || typ == FieldInteger {
		return parseInteger(numberText(text, opts), typ)
	}

	if _, ok := signedLimits[typ]; ok {
		return parseInteger(numberText(text, opts), typ)
	}

	if isDecimal(typ) {
		number := numberText(text, opts)

		return parseDecimal(number)
	}

	//nolint:exhaustive // The numeric types are parsed above, the others by ParseField.
	switch typ {
	case FieldBoolean:
		return strconv.ParseBool(text) //nolint:wrapcheck // ParseField wraps it.
	case FieldNumber, FieldDouble:
		return parseNumber(numberText(text, opts))
	case FieldFloat64:
		return parseFloat(numberText(text, opts), float64Bits)
	case FieldFloat32:
		number, err := parseFloat(numberText(text, opts), float32Bits)

		return float32(number), err
	case FieldVarbinary:
		return parseVarbinary(text, opts)
	case FieldUUID:
		return uuid.Parse(text) //nolint:wrapcheck // ParseField wraps it.
	case FieldDatetime:
		return ParseDatetime(text)
	case FieldInterval:
		return ParseInterval(text)
	case FieldArray, FieldMap:
		value, err := ParseTaggedJSON([]byte(text))
		if err == nil && !fits(value, typ) {
			return nil, ErrFieldValue
		}

		return value, err
	}

	return nil, ErrFieldType
}

// parseVarbinary parses the text of a varbinary field: its bytes as they are
// with raw options, otherwise their base64, which may be in the double
// quotes of JSON, as the JSON of a []byte is.
func parseVarbinary(text string, opts FieldOptions) (any, error) {
	if opts.RawVarbinary {
		return Binary(text), nil
	}

	if inner, ok := unquoteJSON(text); ok {
		text = inner
	}

	data, err := base64.StdEncoding.DecodeString(text)

	return Binary(data), err
}

// numberText is text with the thousand separators of opts dropped and its
// decimal separators replaced with a point.
func numberText(text string, opts FieldOptions) string {
	if opts.ThousandSeparators == "" && opts.DecimalSeparators == "" {
		return text
	}

	return strings.Map(func(char rune) rune {
		switch {
		case strings.ContainsRune(opts.ThousandSeparators, char):
			return -1
		case strings.ContainsRune(opts.DecimalSeparators, char):
			return '.'
		}

		return char
	}, text)
}

// parseNumber parses a number: an integer, or else a float64.
func parseNumber(text string) (any, error) {
	value, err := parseInteger(text, FieldInteger)
	if err == nil {
		return value, nil
	}

	return parseFloat(text, float64Bits)
}

var (
	// errFloatText reports a text that is not a float in decimal, NaN or an
	// infinity.
	errFloatText = errors.New("not a float")
	// errUnderflow reports a number too small for a float, which strconv
	// reads as 0.
	errUnderflow = errors.New("too small for a float")
)

// parseFloat parses the text of a float of bitSize bits: a float in decimal,
// or NaN or an infinity in any case, such as nan, -inf or Infinity. The
// hexadecimal floats and underscores strconv takes besides are an error, as is
// a number too small for the float, which strconv reads as 0, and one too
// large.
func parseFloat(text string, bitSize int) (float64, error) {
	if !isFloatName(text) && !isDecimalFloat(text) {
		return 0, fmt.Errorf("%q: %w", text, errFloatText)
	}

	number, err := strconv.ParseFloat(text, bitSize)
	if err != nil {
		return 0, err //nolint:wrapcheck // ParseField wraps it.
	}

	return number, checkUnderflow(number, text)
}

// checkUnderflow reports a number strconv read from text as 0 where text is
// not 0.
func checkUnderflow(number float64, text string) error {
	if number != 0 {
		return nil
	}

	mantissa, _, _ := strings.Cut(strings.ToLower(text), "e")
	if strings.Trim(mantissa, "+-.0") != "" {
		return fmt.Errorf("%q: %w", text, errUnderflow)
	}

	return nil
}

// isFloatName reports whether text is NaN or an infinity, in any case and
// with an optional sign.
func isFloatName(text string) bool {
	if text != "" && (text[0] == '-' || text[0] == '+') {
		text = text[1:]
	}

	return strings.EqualFold(text, "nan") || strings.EqualFold(text, "inf") ||
		strings.EqualFold(text, "infinity")
}

// isDecimalFloat reports whether text is a float in decimal: an optional
// sign, digits with an optional point or a point and digits, and an optional
// exponent.
func isDecimalFloat(text string) bool {
	scan := scanner{text: text, pos: 0}
	if !scan.skip('-') {
		scan.skip('+')
	}

	whole := scan.skipDigits()
	if scan.skip('.') {
		if !scan.skipDigits() && !whole {
			return false
		}
	} else if !whole {
		return false
	}

	if scan.skip('e') || scan.skip('E') {
		if !scan.skip('-') {
			scan.skip('+')
		}

		if !scan.skipDigits() {
			return false
		}
	}

	return scan.done()
}

// parseInteger parses an integer of an integer type: a uint64, or an int64
// when it is negative.
func parseInteger(text string, typ FieldType) (any, error) {
	unsigned, err := strconv.ParseUint(text, decimalBase, 64)
	if err == nil {
		if fits(unsigned, typ) {
			return unsigned, nil
		}

		return nil, ErrFieldValue
	}

	signed, err := strconv.ParseInt(text, decimalBase, 64)
	if err != nil {
		return nil, err //nolint:wrapcheck // ParseField wraps it.
	}

	if signed >= 0 {
		// -0 is the only text ParseUint does not take and ParseInt reads as
		// zero.
		return parseInteger(strconv.FormatInt(signed, decimalBase), typ)
	}

	if !fits(signed, typ) {
		return nil, ErrFieldValue
	}

	return signed, nil
}

// fitsSized reports whether value is an integer in the range of an integer
// type of a fixed size, or of unsigned; known is false for any other type.
func fitsSized(value any, typ FieldType) (bool, bool) {
	if limit, ok := unsignedLimits[typ]; ok {
		unsigned, isUint := value.(uint64)

		return isUint && unsigned <= limit, true
	}

	limits, ok := signedLimits[typ]
	if !ok {
		return false, false
	}

	if unsigned, isUint := value.(uint64); isUint {
		return unsigned <= uint64(limits[1]), true //nolint:gosec // The upper limit is positive.
	}

	signed, isInt := value.(int64)

	return isInt && signed >= limits[0] && signed <= limits[1], true
}

// fits reports whether value, as Decode or ParseTaggedJSON return it, is a
// value of a field of type typ, as Tarantool tells it (field_mp_type and
// field_ext_type in src/box/field_def.c).
//
//nolint:cyclop // One case per type is the clearest form.
func fits(value any, typ FieldType) bool {
	if fit, known := fitsSized(value, typ); known {
		return fit
	}

	numeric := typ == FieldNumber || typ == FieldScalar || typ == FieldAny

	switch typed := value.(type) {
	case uint64, int64:
		return numeric || typ == FieldInteger || typ == FieldDouble
	case float64:
		return numeric || typ == FieldDouble || typ == FieldFloat64
	case float32:
		return numeric || typ == FieldDouble || typ == FieldFloat32
	case decimal.Decimal:
		return numeric || isDecimal(typ)
	case []any:
		return typ == FieldArray || typ == FieldAny
	case Map:
		return typ == FieldMap || typ == FieldAny
	case datetime.Interval:
		// A scalar takes no interval.
		return typ == FieldInterval || typ == FieldAny
	case Ext:
		return typ == FieldAny && validExt(typed)
	}

	return typ == FieldScalar || typ == FieldAny || typ == scalarType(value)
}

// validExt reports whether Tarantool takes an extension Decode keeps as an
// Ext in a field of type any: one of a positive type, and not one of a type
// Decode knows, which it decodes unless its payload is malformed.
func validExt(ext Ext) bool {
	switch ext.Type {
	case ExtDecimal, ExtUUID, ExtDatetime, ExtInterval:
		return false
	}

	return ext.Type > 0
}

// scalarType is the field type of a scalar value of no numeric type.
func scalarType(value any) FieldType {
	switch value.(type) {
	case bool:
		return FieldBoolean
	case string:
		return FieldString
	case Binary:
		return FieldVarbinary
	case uuid.UUID:
		return FieldUUID
	case datetime.Datetime:
		return FieldDatetime
	}

	return ""
}

// guessValue guesses the type of the value of an any or scalar field by its
// text.
func guessValue(text string, field Field, opts FieldOptions) any {
	if value, ok := guessNumber(text, opts); ok {
		return value
	}

	switch text {
	case "true":
		return true
	case "false":
		return false
	}

	if value, ok := guessExtension(text, field.Type, opts); ok {
		return value
	}

	if strings.HasPrefix(text, "[") || strings.HasPrefix(text, "{") ||
		strings.HasPrefix(text, `"`) {
		if value, ok := taggedOf(text, field.Type); ok {
			return value
		}
	}

	return text
}

// legacyInfinities are the infinities as fmt.Sprint writes them, as the
// export of tt-ee on go-tupleconv did.
var legacyInfinities = map[string]bool{"+Inf": true, "-Inf": true, "Inf": true}

// guessNumber reads text as a number if it is one as JSON writes it, NaN,
// Infinity or -Infinity, or an infinity as fmt.Sprint writes it.
func guessNumber(text string, opts FieldOptions) (any, bool) {
	if isFloatWord(text) || legacyInfinities[text] {
		number, _ := strconv.ParseFloat(text, float64Bits)

		return number, true
	}

	number := numberText(text, opts)
	if !isJSONNumber(number) {
		return nil, false
	}

	value, err := jsonNumber(number)

	return value, err == nil
}

// guessExtension reads text as a datetime with a time zone, a UUID in its
// canonical form or, in a field of type typ that takes one, an interval that
// starts with a sign or is in the text of go-tupleconv, of an extension type
// opts do not leave out.
func guessExtension(text string, typ FieldType, opts FieldOptions) (any, bool) {
	if supported(opts, ExtDatetime) {
		value, err := parseDatetime(text, true)
		if err == nil {
			return value, true
		}
	}

	if supported(opts, ExtUUID) && len(text) == len(uuid.Nil.String()) {
		value, err := uuid.Parse(text)
		if err == nil && value.String() == text {
			return value, true
		}
	}

	signed := strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-")
	legacy := strings.Count(text, ",") == legacyIntervalFields-1

	if typ == FieldAny && supported(opts, ExtInterval) && (signed || legacy) {
		value, err := ParseInterval(text)
		if err == nil {
			return value, true
		}
	}

	return nil, false
}

// supported reports whether the extension type typ is not in
// opts.Unsupported.
func supported(opts FieldOptions, typ int8) bool {
	return !slices.Contains(opts.Unsupported, typ)
}

// isJSONNumber reports whether text is a number as JSON writes it: an
// optional minus, an integer with no leading zero, an optional fraction and
// an optional exponent.
func isJSONNumber(text string) bool {
	scan := scanner{text: text, pos: 0}
	scan.skip('-')

	if !scan.skip('0') {
		if !scan.peekAt(0, isDigit) {
			return false
		}

		scan.skipDigits()
	}

	if scan.skip('.') && !scan.skipDigits() {
		return false
	}

	if scan.skip('e') || scan.skip('E') {
		if !scan.skip('+') {
			scan.skip('-')
		}

		if !scan.skipDigits() {
			return false
		}
	}

	return scan.done()
}
