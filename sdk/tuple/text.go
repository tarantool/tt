package tuple

import (
	"encoding/base64"
	"math"
	"strconv"
	"time"

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
//   - A decimal is its decimal text, a UUID its canonical form, a datetime
//     RFC 3339 with nanoseconds and the offset of its time zone, an interval
//     the text Tarantool prints, such as "+1 years, -2 seconds".
//   - An [Ext] is "ext:" followed by its type, a colon and the base64 of its
//     payload.
//
//nolint:cyclop // One case per type is the clearest form.
func Text(value any) (string, bool) {
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
		return typed.String(), true
	case *datetime.Interval:
		return typed.String(), true
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

// decimalText is d with as many digits after the point as its scale: 12.340
// stays 12.340, where go-tarantool would print 12.34.
func decimalText(d decimal.Decimal) string {
	if exponent := d.Exponent(); exponent < 0 {
		return d.StringFixed(-exponent)
	}

	return d.String()
}

// datetimeText is d in RFC 3339 with nanoseconds.
func datetimeText(d datetime.Datetime) string {
	return d.ToTime().Format(time.RFC3339Nano)
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
// same float of bitSize bits, with no exponent unless its magnitude is below
// 1e-6 or from 1e21 up, as encoding/json writes it: a timestamp is
// 1731592956.818, not 1.731592956818e+09.
func formatFloat(number float64, bitSize int) string {
	format := byte('f')
	if abs := math.Abs(number); abs != 0 && (abs < minPlainFloat || abs >= maxPlainFloat) {
		format = 'e'
	}

	return strconv.FormatFloat(number, format, -1, bitSize)
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
