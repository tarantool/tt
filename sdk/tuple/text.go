package tuple

import (
	"encoding/base64"
	"math"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
)

// Spellings of the floats that have no number in JSON, as
// github.com/tarantool/tt/sdk/output writes them.
const (
	textNaN    = "NaN"
	textInf    = "Infinity"
	textNegInf = "-Infinity"
)

// The bit sizes of the floats as strconv takes them, and the base integers
// are written in.
const (
	float32Bits = 32
	float64Bits = 64
	decimalBase = 10
)

// Text returns the text of a scalar value, and false for a value that has
// none: nil, an array, a map, a type this package does not render.
//
//   - A bool is true or false, an integer is in decimal, a float is the
//     shortest decimal that reads back as the same float, or NaN, Infinity
//     or -Infinity.
//   - A string is itself, a [Binary] or a []byte its standard base64.
//   - A decimal is the text Tarantool prints, which keeps its digits and its
//     scale: 12.340, 1E+2, 1E-7. A UUID is its canonical form.
//   - A datetime is the text Tarantool prints, such as
//     "2024-11-14T14:02:36.027 Europe/Moscow" or "2024-11-14T11:02:36Z",
//     which [ParseDatetime] and datetime.parse read back. Like the text of
//     Tarantool, it does not tell apart a time zone named Z from none, nor the
//     two instants of the hour a named zone repeats when its clocks go back;
//     [AppendTaggedJSON] does.
//   - An interval is the text Tarantool prints with its adjust mode when it
//     is not none, such as "+1 years, -2 seconds, adjust last", which
//     [ParseInterval] reads back.
//   - An [Ext] is "ext:" followed by its type, a colon and the base64 of its
//     payload.
//
//nolint:cyclop // One case per type is the clearest form.
func Text(value any) (string, bool) {
	if isNil(value) {
		return "", false
	}

	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed), true
	case string:
		return typed, true
	case Binary:
		return base64.StdEncoding.EncodeToString(typed), true
	case []byte:
		return base64.StdEncoding.EncodeToString(typed), true
	case float32:
		return floatText(float64(typed), float32Bits), true
	case float64:
		return floatText(typed, float64Bits), true
	case decimal.Decimal:
		return decimalText(typed), true
	case *decimal.Decimal:
		return decimalText(*typed), true
	case uuid.UUID:
		return typed.String(), true
	case datetime.Datetime:
		return datetimeText(typed), true
	case *datetime.Datetime:
		return datetimeText(*typed), true
	case datetime.Interval:
		return intervalText(typed), true
	case *datetime.Interval:
		return intervalText(*typed), true
	case Ext:
		return "ext:" + strconv.Itoa(int(typed.Type)) + ":" +
			base64.StdEncoding.EncodeToString(typed.Data), true
	}

	if i, ok := asInt(value); ok {
		return strconv.FormatInt(i, decimalBase), true
	}

	if u, ok := asUint(value); ok {
		return strconv.FormatUint(u, decimalBase), true
	}

	return "", false
}

// decimalText is number as Tarantool prints it, the to-scientific-string of
// the General Decimal Arithmetic: its digits with a point at its scale, 12.340
// where go-tarantool would print 12.34, unless its exponent is positive or
// its first digit is more than six places after the point, as in 1E+2,
// 1.0E+3 or 1E-7. The text keeps the digits and the exponent of number, so a
// decimal of an exponent of millions takes a few bytes.
func decimalText(number decimal.Decimal) string {
	const minPlainExponent = -6

	digits := decimalDigits(number)
	exponent := int64(number.Exponent())
	adjusted := exponent + int64(len(digits)) - 1

	var out []byte

	if number.Sign() < 0 {
		out = append(out, '-')
	}

	switch point := int64(len(digits)) + exponent; {
	case exponent == 0:
		out = append(out, digits...)
	case exponent < 0 && adjusted >= minPlainExponent && point > 0:
		out = append(append(append(out, digits[:point]...), '.'), digits[point:]...)
	case exponent < 0 && adjusted >= minPlainExponent:
		out = append(out, "0."...)
		out = append(out, strings.Repeat("0", int(-point))...)
		out = append(out, digits...)
	default:
		out = append(out, digits[0])
		if len(digits) > 1 {
			out = append(append(out, '.'), digits[1:]...)
		}

		out = append(out, 'E')
		if adjusted >= 0 {
			out = append(out, '+')
		}

		out = strconv.AppendInt(out, adjusted, decimalBase)
	}

	return string(out)
}

// floatText is number as the shortest decimal that reads back as the same float
// of bitSize bits.
func floatText(number float64, bitSize int) string {
	switch {
	case math.IsNaN(number):
		return textNaN
	case math.IsInf(number, 1):
		return textInf
	case math.IsInf(number, -1):
		return textNegInf
	}

	return formatFloat(number, bitSize)
}

// Bounds of the magnitudes formatFloat writes without an exponent, those of
// encoding/json.
const (
	minPlainFloat = 1e-6
	maxPlainFloat = 1e21
)

// formatFloat is a finite number as the shortest decimal that reads back as the
// same float of bitSize bits, as encoding/json writes it: with no exponent
// unless its magnitude, as a float of that size, is below 1e-6 or from 1e21
// up, and with no leading zero in a negative exponent. A timestamp is
// 1731592956.818, not 1.731592956818e+09, and 1e-7 is not 1e-07.
func formatFloat(number float64, bitSize int) string {
	abs := math.Abs(number)

	plain := abs == 0 || (abs >= minPlainFloat && abs < maxPlainFloat)
	if bitSize == float32Bits {
		small := float32(abs)

		plain = abs == 0 || (small >= minPlainFloat && small < maxPlainFloat)
	}

	if plain {
		return strconv.FormatFloat(number, 'f', -1, bitSize)
	}

	text := strconv.FormatFloat(number, 'e', -1, bitSize)

	if n := len(text); n >= 4 && text[n-4] == 'e' && text[n-3] == '-' && text[n-2] == '0' {
		text = text[:n-2] + text[n-1:]
	}

	return text
}

// asInt returns a signed integer of any width as an int64.
func asInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	}

	return 0, false
}

// asUint returns an unsigned integer of any width as a uint64.
func asUint(value any) (uint64, bool) {
	switch typed := value.(type) {
	case uint:
		return uint64(typed), true
	case uint8:
		return uint64(typed), true
	case uint16:
		return uint64(typed), true
	case uint32:
		return uint64(typed), true
	case uint64:
		return typed, true
	}

	return 0, false
}
