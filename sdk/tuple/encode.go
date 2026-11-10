package tuple

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
)

// The nibbles that end the digits of a decimal with its sign (mp_decimal of
// Tarantool, the packed decimal of decNumber).
const (
	decimalPlus  = 0x0c
	decimalMinus = 0x0d
	nibbleBits   = 4
)

// The most entries the fix codes of MessagePack hold.
const (
	fixStrMax   = 31
	fixArrayMax = 15
)

// errEncodeDecimal reports a decimal Tarantool does not hold.
var errEncodeDecimal = errors.New("a decimal Tarantool does not hold")

// AppendMsgpack appends the MessagePack of value, the inverse of [Decode]:
// Decode reads back the value, and Tarantool stores it as it is.
//
//   - nil, a bool, a string, a [Binary] and an array are their MessagePack
//     type; an integer is MP_UINT if it is not negative and MP_INT if it is,
//     a float32 MP_FLOAT, a float64 MP_DOUBLE, each in its shortest form.
//   - A [Map] keeps the order and the repeats of its keys.
//   - A decimal keeps its digits and its scale, which go-tarantool would
//     trim: 12.340 stays 12.340. A UUID, a datetime and an interval are the
//     extensions of Tarantool, an [Ext] its type and payload as they are.
//
// It takes the values go-tarantool decodes as well: integers and floats of
// any width, []byte, slices, Go maps, whose entries it sorts as the other
// renderers do, and pointers. A value of another type, or a decimal
// Tarantool does not hold, is an error, and dst is returned as it is then.
func AppendMsgpack(dst []byte, value any) ([]byte, error) {
	out, err := appendMsgpack(dst, value)
	if err != nil {
		return dst, err
	}

	return out, nil
}

//nolint:cyclop,funlen // One case per type is the clearest form.
func appendMsgpack(dst []byte, value any) ([]byte, error) {
	if isNil(value) {
		return append(dst, codeNil), nil
	}

	switch typed := value.(type) {
	case bool:
		if typed {
			return append(dst, codeTrue), nil
		}

		return append(dst, codeFalse), nil
	case string:
		return appendMsgpackBytes(dst, []byte(typed), codeFixstr, codeStr8), nil
	case Binary:
		return appendMsgpackBytes(dst, typed, 0, codeBin8), nil
	case []byte:
		return appendMsgpackBytes(dst, typed, 0, codeBin8), nil
	case float32:
		return binary.BigEndian.AppendUint32(append(dst, codeFloat32), math.Float32bits(typed)), nil
	case float64:
		return binary.BigEndian.AppendUint64(append(dst, codeFloat64), math.Float64bits(typed)), nil
	case decimal.Decimal:
		return appendMsgpackDecimal(dst, typed)
	case *decimal.Decimal:
		return appendMsgpackDecimal(dst, *typed)
	case uuid.UUID:
		return AppendExtMsgpack(dst, ExtUUID, typed[:]), nil
	case datetime.Datetime:
		return appendMsgpackMarshaler(dst, ExtDatetime, typed.MarshalMsgpack)
	case *datetime.Datetime:
		return appendMsgpackMarshaler(dst, ExtDatetime, typed.MarshalMsgpack)
	case datetime.Interval:
		return appendMsgpackMarshaler(dst, ExtInterval, typed.MarshalMsgpack)
	case *datetime.Interval:
		return appendMsgpackMarshaler(dst, ExtInterval, typed.MarshalMsgpack)
	case Ext:
		return AppendExtMsgpack(dst, typed.Type, typed.Data), nil
	case []any:
		return appendMsgpackArray(dst, len(typed), func(i int) any { return typed[i] })
	case Map:
		return appendMsgpackMap(dst, typed)
	case map[any]any:
		return appendMsgpackMap(dst, sortedMap(typed))
	case map[string]any:
		items := make(Map, 0, len(typed))
		for key, item := range typed {
			items = append(items, MapItem{Key: key, Value: item})
		}

		return appendMsgpackMap(dst, sortItems(items))
	}

	if signed, ok := asInt(value); ok {
		return appendMsgpackInt(dst, signed), nil
	}

	if unsigned, ok := asUint(value); ok {
		return appendMsgpackUint(dst, unsigned), nil
	}

	return appendMsgpackReflect(dst, value)
}

// appendMsgpackReflect appends a pointer by what it points to, and a slice,
// an array or a map of another type by its items.
func appendMsgpackReflect(dst []byte, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)

	switch reflected.Kind() { //nolint:exhaustive // Other kinds have no MessagePack.
	case reflect.Pointer, reflect.Interface:
		if reflected.IsNil() {
			return append(dst, codeNil), nil
		}

		return appendMsgpack(dst, reflected.Elem().Interface())
	case reflect.Slice, reflect.Array:
		return appendMsgpackArray(dst, reflected.Len(), func(i int) any {
			return reflected.Index(i).Interface()
		})
	case reflect.Map:
		items := make(Map, 0, reflected.Len())
		for iter := reflected.MapRange(); iter.Next(); {
			items = append(items, MapItem{
				Key:   iter.Key().Interface(),
				Value: iter.Value().Interface(),
			})
		}

		return appendMsgpackMap(dst, sortItems(items))
	}

	return dst, fmt.Errorf("%w: %T", ErrUnsupportedType, value)
}

// appendMsgpackUint appends an MP_UINT in its shortest form.
func appendMsgpackUint(dst []byte, value uint64) []byte {
	switch {
	case value <= codePositiveFixintMax:
		return append(dst, byte(value))
	case value <= math.MaxUint8:
		return append(dst, codeUint8, byte(value))
	case value <= math.MaxUint16:
		return binary.BigEndian.AppendUint16(append(dst, codeUint16), uint16(value))
	case value <= math.MaxUint32:
		return binary.BigEndian.AppendUint32(append(dst, codeUint32), uint32(value))
	}

	return binary.BigEndian.AppendUint64(append(dst, codeUint64), value)
}

// appendMsgpackInt appends an integer: an MP_UINT if it is not negative, an
// MP_INT in its shortest form if it is.
//
//nolint:gosec // The conversions keep the bits of a value that fits them.
func appendMsgpackInt(dst []byte, value int64) []byte {
	const negativeFixintMin = -32

	switch {
	case value >= 0:
		return appendMsgpackUint(dst, uint64(value))
	case value >= negativeFixintMin:
		return append(dst, byte(int8(value)))
	case value >= math.MinInt8:
		return append(dst, codeInt8, byte(int8(value)))
	case value >= math.MinInt16:
		return binary.BigEndian.AppendUint16(append(dst, codeInt16), uint16(int16(value)))
	case value >= math.MinInt32:
		return binary.BigEndian.AppendUint32(append(dst, codeInt32), uint32(int32(value)))
	}

	return binary.BigEndian.AppendUint64(append(dst, codeInt64), uint64(value))
}

// appendMsgpackBytes appends a string or a binary value: fix is the fix code
// of a string, 0 for a binary value, which has none; code8 the code of the
// form with a one-byte length, which those with two and four follow.
//
//nolint:gosec // A length that fits the form is written.
func appendMsgpackBytes(dst, data []byte, fix, code8 byte) []byte {
	const (
		code16 = 1
		code32 = 2
	)

	size := len(data)

	switch {
	case fix != 0 && size <= fixStrMax:
		dst = append(dst, fix|byte(size))
	case size <= math.MaxUint8:
		dst = append(dst, code8, byte(size))
	case size <= math.MaxUint16:
		dst = binary.BigEndian.AppendUint16(append(dst, code8+code16), uint16(size))
	default:
		dst = binary.BigEndian.AppendUint32(append(dst, code8+code32), uint32(size))
	}

	return append(dst, data...)
}

// appendMsgpackHeader appends the header of an array or a map of count
// items.
//
//nolint:gosec // A count that fits the form is written.
func appendMsgpackHeader(dst []byte, count int, fix, code16 byte) []byte {
	switch {
	case count <= fixArrayMax:
		return append(dst, fix|byte(count))
	case count <= math.MaxUint16:
		return binary.BigEndian.AppendUint16(append(dst, code16), uint16(count))
	}

	return binary.BigEndian.AppendUint32(append(dst, code16+1), uint32(count))
}

func appendMsgpackArray(dst []byte, count int, item func(int) any) ([]byte, error) {
	dst = appendMsgpackHeader(dst, count, codeFixarray, codeArray16)

	for index := range count {
		var err error

		dst, err = appendMsgpack(dst, item(index))
		if err != nil {
			return dst, err
		}
	}

	return dst, nil
}

func appendMsgpackMap(dst []byte, items Map) ([]byte, error) {
	dst = appendMsgpackHeader(dst, len(items), codeFixmap, codeMap16)

	for _, item := range items {
		var err error

		dst, err = appendMsgpack(dst, item.Key)
		if err != nil {
			return dst, err
		}

		dst, err = appendMsgpack(dst, item.Value)
		if err != nil {
			return dst, err
		}
	}

	return dst, nil
}

// appendMsgpackDecimal appends a decimal as Tarantool packs it: its scale,
// then its digits two to a byte with the sign in the last nibble and a zero
// before them if they are even.
func appendMsgpackDecimal(dst []byte, value decimal.Decimal) ([]byte, error) {
	if !validDecimal(value) {
		return dst, fmt.Errorf("%s: %w", decimalText(value), errEncodeDecimal)
	}

	digits := decimalDigits(value)

	// The digits and the sign, padded to whole bytes.
	nibbles := make([]byte, 0, len(digits)+1+1)
	if len(digits)%2 == 0 {
		nibbles = append(nibbles, 0)
	}

	for index := range len(digits) {
		nibbles = append(nibbles, digits[index]-'0')
	}

	sign := byte(decimalPlus)
	if value.Sign() < 0 {
		sign = decimalMinus
	}

	nibbles = append(nibbles, sign)

	payload := appendMsgpackInt(nil, -int64(value.Exponent()))
	for pair := range slices.Chunk(nibbles, 1+1) {
		payload = append(payload, pair[0]<<nibbleBits|pair[1])
	}

	return AppendExtMsgpack(dst, ExtDecimal, payload), nil
}

// appendMsgpackMarshaler appends an extension of type typ whose payload
// marshal makes.
func appendMsgpackMarshaler(dst []byte, typ int8, marshal func() ([]byte, error)) ([]byte, error) {
	payload, err := marshal()
	if err != nil {
		return dst, fmt.Errorf("encoding extension %s: %w", strconv.Itoa(int(typ)), err)
	}

	return AppendExtMsgpack(dst, typ, payload), nil
}
