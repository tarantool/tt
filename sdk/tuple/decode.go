package tuple

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
)

// Extension types Tarantool defines (mp_extension_type in
// src/lib/core/mp_extension_types.h).
const (
	ExtDecimal     int8 = 1
	ExtUUID        int8 = 2
	ExtError       int8 = 3
	ExtDatetime    int8 = 4
	ExtCompression int8 = 5
	ExtInterval    int8 = 6
	ExtTuple       int8 = 7
)

// Sizes of the payload of a datetime: the seconds alone, or the seconds with
// the nanoseconds, the offset and the time zone index.
const (
	datetimeShortSize = 8
	datetimeFullSize  = 16
	uuidSize          = 16
)

// The MessagePack format codes, as the specification names them
// (https://github.com/msgpack/msgpack/blob/master/spec.md).
const (
	codePositiveFixintMax = 0x7f
	codeFixmap            = 0x80
	codeFixmapMax         = 0x8f
	codeFixarray          = 0x90
	codeFixarrayMax       = 0x9f
	codeFixstr            = 0xa0
	codeFixstrMax         = 0xbf
	codeNil               = 0xc0
	codeFalse             = 0xc2
	codeTrue              = 0xc3
	codeBin8              = 0xc4
	codeBin16             = 0xc5
	codeBin32             = 0xc6
	codeExt8              = 0xc7
	codeExt16             = 0xc8
	codeExt32             = 0xc9
	codeFloat32           = 0xca
	codeFloat64           = 0xcb
	codeUint8             = 0xcc
	codeUint16            = 0xcd
	codeUint32            = 0xce
	codeUint64            = 0xcf
	codeInt8              = 0xd0
	codeInt16             = 0xd1
	codeInt32             = 0xd2
	codeInt64             = 0xd3
	codeFixext1           = 0xd4
	codeFixext2           = 0xd5
	codeFixext4           = 0xd6
	codeFixext8           = 0xd7
	codeFixext16          = 0xd8
	codeStr8              = 0xd9
	codeStr16             = 0xda
	codeStr32             = 0xdb
	codeArray16           = 0xdc
	codeArray32           = 0xdd
	codeMap16             = 0xde
	codeMap32             = 0xdf
	codeNegativeFixintMin = 0xe0

	// fixLengthMask takes the length of a fixmap or a fixarray out of its
	// code, fixstrLengthMask that of a fixstr.
	fixLengthMask    = 0x0f
	fixstrLengthMask = 0x1f
)

// The sizes in bytes of the integers, floats and lengths MessagePack holds,
// and of the payload of a fixext 16.
const (
	size8   = 1
	size16  = 2
	size32  = 4
	size64  = 8
	size128 = 16
)

// maxDepth bounds the nesting of the values Decode accepts, so that a
// malformed value cannot exhaust the stack.
const maxDepth = 512

var (
	// ErrTruncated reports MessagePack that ends inside a value.
	ErrTruncated = errors.New("msgpack: value is truncated")
	// ErrInvalid reports a byte that starts no MessagePack value, or a value
	// nested deeper than Decode accepts.
	ErrInvalid = errors.New("msgpack: invalid value")
	// ErrTrailing reports bytes left after the value Decode was given.
	ErrTrailing = errors.New("msgpack: bytes after the value")
)

// Binary is an MP_BIN value. It renders as base64 in JSON, as a !!binary
// scalar in YAML and through the varbinary module in Lua.
type Binary []byte

// MapItem is an entry of a [Map].
type MapItem struct {
	Key   any
	Value any
}

// Map is an MP_MAP value with its entries in the order MessagePack holds
// them.
type Map []MapItem

// Ext is an extension value this package does not decode into a type of its
// own: its extension type and its payload.
type Ext struct {
	Type int8
	Data []byte
}

// Decode decodes data, which must hold exactly one MessagePack value.
func Decode(data []byte) (any, error) {
	value, size, err := DecodeNext(data)
	if err != nil {
		return nil, err
	}

	if size != len(data) {
		return nil, fmt.Errorf("%w: %d of %d bytes", ErrTrailing, len(data)-size, len(data))
	}

	return value, nil
}

// DecodeNext decodes the MessagePack value data starts with and returns it with
// the number of bytes it takes.
func DecodeNext(data []byte) (any, int, error) {
	dec := decoder{buf: data, pos: 0}

	value, err := dec.value(0)
	if err != nil {
		return nil, 0, err
	}

	return value, dec.pos, nil
}

// decoder reads MessagePack values from buf, starting at pos.
type decoder struct {
	buf []byte
	pos int
}

// take returns the next count bytes and moves past them.
func (d *decoder) take(count int) ([]byte, error) {
	if count < 0 || len(d.buf)-d.pos < count {
		return nil, ErrTruncated
	}

	out := d.buf[d.pos : d.pos+count]

	d.pos += count

	return out, nil
}

func (d *decoder) uint(size int) (uint64, error) {
	raw, err := d.take(size)
	if err != nil {
		return 0, err
	}

	switch size {
	case size8:
		return uint64(raw[0]), nil
	case size16:
		return uint64(binary.BigEndian.Uint16(raw)), nil
	case size32:
		return uint64(binary.BigEndian.Uint32(raw)), nil
	default:
		return binary.BigEndian.Uint64(raw), nil
	}
}

// length reads a length of size bytes, which must fit an int.
func (d *decoder) length(size int) (int, error) {
	value, err := d.uint(size)
	if err != nil {
		return 0, err
	}

	if value > uint64(len(d.buf)) {
		return 0, ErrTruncated
	}

	return int(value), nil //nolint:gosec // Checked against the length of the buffer.
}

//nolint:cyclop,funlen,gocyclo // One case per MessagePack code is the clearest form.
func (d *decoder) value(depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: nested deeper than %d", ErrInvalid, maxDepth)
	}

	head, err := d.take(1)
	if err != nil {
		return nil, err
	}

	code := head[0]

	switch {
	case code <= codePositiveFixintMax:
		return uint64(code), nil
	case code >= codeNegativeFixintMin:
		return int64(int8(code)), nil //nolint:gosec // A negative fixint is two's complement.
	case code >= codeFixmap && code <= codeFixmapMax:
		return d.mapOf(int(code&fixLengthMask), depth)
	case code >= codeFixarray && code <= codeFixarrayMax:
		return d.arrayOf(int(code&fixLengthMask), depth)
	case code >= codeFixstr && code <= codeFixstrMax:
		return d.str(int(code & fixstrLengthMask))
	}

	switch code {
	case codeNil:
		return nil, nil //nolint:nilnil // MP_NIL is nil.
	case codeFalse:
		return false, nil
	case codeTrue:
		return true, nil
	case codeBin8, codeBin16, codeBin32:
		n, err := d.length(1 << (code - codeBin8))
		if err != nil {
			return nil, err
		}

		b, err := d.take(n)
		if err != nil {
			return nil, err
		}

		return Binary(clone(b)), nil
	case codeExt8, codeExt16, codeExt32:
		n, err := d.length(1 << (code - codeExt8))
		if err != nil {
			return nil, err
		}

		return d.ext(n)
	case codeFloat32:
		raw, err := d.take(size32)
		if err != nil {
			return nil, err
		}

		return math.Float32frombits(binary.BigEndian.Uint32(raw)), nil
	case codeFloat64:
		bits, err := d.uint(size64)
		if err != nil {
			return nil, err
		}

		return math.Float64frombits(bits), nil
	case codeUint8, codeUint16, codeUint32, codeUint64:
		return d.uint(1 << (code - codeUint8))
	case codeInt8, codeInt16, codeInt32, codeInt64:
		size := 1 << (code - codeInt8)

		bits, err := d.uint(size)
		if err != nil {
			return nil, err
		}

		return signExtend(bits, size), nil
	case codeFixext1, codeFixext2, codeFixext4, codeFixext8, codeFixext16:
		return d.ext(1 << (code - codeFixext1))
	case codeStr8, codeStr16, codeStr32:
		n, err := d.length(1 << (code - codeStr8))
		if err != nil {
			return nil, err
		}

		return d.str(n)
	case codeArray16, codeArray32:
		n, err := d.length(size16 << (code - codeArray16))
		if err != nil {
			return nil, err
		}

		return d.arrayOf(n, depth)
	case codeMap16, codeMap32:
		n, err := d.length(size16 << (code - codeMap16))
		if err != nil {
			return nil, err
		}

		return d.mapOf(n, depth)
	}

	return nil, fmt.Errorf("%w: code 0x%02x at offset %d", ErrInvalid, code, d.pos-1)
}

// signExtend reads the bits of a signed integer of size bytes as two's
// complement.
//
//nolint:gosec // The conversions reinterpret the bits, as they are meant to.
func signExtend(bits uint64, size int) int64 {
	switch size {
	case size8:
		return int64(int8(bits))
	case size16:
		return int64(int16(bits))
	case size32:
		return int64(int32(bits))
	default:
		return int64(bits)
	}
}

func (d *decoder) str(n int) (any, error) {
	b, err := d.take(n)
	if err != nil {
		return nil, err
	}

	return string(b), nil
}

func (d *decoder) arrayOf(count int, depth int) (any, error) {
	if count > len(d.buf)-d.pos {
		return nil, ErrTruncated
	}

	out := make([]any, 0, count)

	for range count {
		item, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}

		out = append(out, item)
	}

	return out, nil
}

func (d *decoder) mapOf(count int, depth int) (any, error) {
	if count > len(d.buf)-d.pos {
		return nil, ErrTruncated
	}

	out := make(Map, 0, count)

	for range count {
		key, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}

		value, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}

		out = append(out, MapItem{Key: key, Value: value})
	}

	return out, nil
}

func (d *decoder) ext(size int) (any, error) {
	head, err := d.take(1)
	if err != nil {
		return nil, err
	}

	data, err := d.take(size)
	if err != nil {
		return nil, err
	}

	return decodeExt(int8(head[0]), clone(data)), nil //nolint:gosec // The type is signed.
}

// decodeExt decodes the payload of an extension of type typ, falling back to
// an Ext for a type it does not know or a payload that does not decode. The
// decoders of go-tarantool trust their input, so a panic on a malformed
// payload is a payload that does not decode as well.
func decodeExt(typ int8, data []byte) any {
	value, ok := decodeKnownExt(typ, data)
	if !ok {
		return Ext{Type: typ, Data: data}
	}

	return value
}

// decodeKnownExt decodes the payload of an extension of a type it knows, and
// reports false for any other or a payload that does not decode: on a panic
// it returns the zero values.
func decodeKnownExt(typ int8, data []byte) (any, bool) {
	defer func() { _ = recover() }()

	switch typ {
	case ExtDecimal:
		var dec decimal.Decimal

		if len(data) > 0 && dec.UnmarshalMsgpack(data) == nil {
			return dec, true
		}
	case ExtUUID:
		if len(data) == uuidSize {
			id, err := uuid.FromBytes(data)
			if err == nil {
				return id, true
			}
		}
	case ExtDatetime:
		var dt datetime.Datetime

		if (len(data) == datetimeShortSize || len(data) == datetimeFullSize) &&
			dt.UnmarshalMsgpack(data) == nil {
			return dt, true
		}
	case ExtInterval:
		var ival datetime.Interval

		if len(data) > 0 && ival.UnmarshalMsgpack(data) == nil {
			return ival, true
		}
	}

	return nil, false
}

func clone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)

	return out
}

// AppendExtMsgpack appends the MessagePack encoding of an extension value:
// the header that fits the length of data, typ and data.
func AppendExtMsgpack(dst []byte, typ int8, data []byte) []byte {
	size := len(data)

	switch {
	case size == size8:
		dst = append(dst, codeFixext1)
	case size == size16:
		dst = append(dst, codeFixext2)
	case size == size32:
		dst = append(dst, codeFixext4)
	case size == size64:
		dst = append(dst, codeFixext8)
	case size == size128:
		dst = append(dst, codeFixext16)
	case size <= math.MaxUint8:
		dst = append(dst, codeExt8, byte(size))
	case size <= math.MaxUint16:
		dst = append(dst, codeExt16)
		dst = binary.BigEndian.AppendUint16(dst, uint16(size))
	default:
		length := uint32(size) //nolint:gosec // An ext payload fits 32 bits.

		dst = binary.BigEndian.AppendUint32(append(dst, codeExt32), length)
	}

	dst = append(dst, byte(typ)) //nolint:gosec // The type is signed: its byte is written.

	return append(dst, data...)
}
