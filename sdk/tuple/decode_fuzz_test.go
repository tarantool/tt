package tuple_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/tuple"
)

// decodeSeeds are MessagePack inputs for FuzzDecode: the canonical
// encodings of the seed values, wide encodings of small values, extensions
// of known types with odd payloads, deep values and malformed ones.
func decodeSeeds(tb testing.TB) [][]byte {
	tb.Helper()

	seeds := [][]byte{
		{}, {0xc1}, {0x92, 0x01}, {0x01, 0x02}, {0xdd, 0xff, 0xff, 0xff, 0xff},
		{0xd0, 0x05}, {0xcf, 0, 0, 0, 0, 0, 0, 0, 5}, {0xd3, 0, 0, 0, 0, 0, 0, 0, 5},
		{0xd9, 0x01, 'a'}, {0xc4, 0x00}, {0xc7, 0x00, 0x42}, {0xd4, 0x02, 0x00},
		{0xd4, 0x04, 0x00}, {0xd5, 0x01, 0xfe, 0x1c}, {0xd5, 0x01, 0x00, 0x12},
		{0xc7, 0x06, 0x01, 0xce, 0x00, 0x0f, 0x42, 0x40, 0x1c},
		{0xc7, 0x06, 0x01, 0xd2, 0x7f, 0xff, 0xff, 0xff, 0x1c},
		{0xd4, 0x06, 0x00}, {0xc7, 0x03, 0x06, 0x01, 0x08, 0x05},
		{0xc7, 0x05, 0x06, 0x01, 0x00, 0xd3, 0x80, 0x00},
		{0xca, 0x7f, 0xc0, 0x00, 0x01}, {0xcb, 0x80, 0, 0, 0, 0, 0, 0, 0},
		{0x82, 0xa1, 'a', 0x01, 0xa1, 'a', 0x02}, {0x81, 0xa4, '$', 'm', 'a', 'p', 0x90},
		bytes.Repeat([]byte{0xdc, 0xff, 0xff}, 600),
		append(bytes.Repeat([]byte{0x91}, decodeDepth), 0xc0),
		append(bytes.Repeat([]byte{0x91}, decodeDepth+1), 0xc0),
		append(bytes.Repeat([]byte{0x81, 0x00}, decodeDepth), 0xc0),
	}

	// A datetime with nanoseconds beyond a second, of an unknown zone, of
	// an offset beyond the range, of the zone Z and of Europe/Berlin with
	// the offset of no time of the zone.
	for _, payload := range []struct {
		seconds       int64
		nanos         uint32
		offset, index int16
	}{
		{0, 1_500_000_000, 0, 0}, {0, 0, 0, 30000}, {0, 0, 15 * 60, 0},
		{1729989000, 0, 0, 25}, {1729989000, 0, 0, 927}, {1729989000, 0, 60, 927},
		{0, 0, -480, 269},
	} {
		raw := datetimePayload(payload.seconds, payload.nanos, payload.offset, payload.index)

		seeds = append(seeds, tuple.AppendExtMsgpack(nil, tuple.ExtDatetime, raw))
	}

	for _, value := range seedValues(tb) {
		seeds = append(seeds, appendMsgpack(tb, nil, value))
	}

	return append(seeds, appendMsgpack(tb, nil, seedValues(tb)))
}

// datetimePayload is the payload of a datetime of its seconds, nanoseconds,
// offset in minutes and index of its time zone.
//
//nolint:gosec // The signed fields are written as their bits.
func datetimePayload(seconds int64, nanos uint32, offset, index int16) []byte {
	order := binary.LittleEndian

	return order.AppendUint16(order.AppendUint16(order.AppendUint32(
		order.AppendUint64(nil, uint64(seconds)), nanos), uint16(offset)), uint16(index))
}

// prefixLengths are the lengths of the prefixes of a value of size bytes
// FuzzDecode checks: all of them for a small value, a sample of a large
// one.
func prefixLengths(size int) []int {
	const most = 512

	if size <= most {
		lengths := make([]int, size)
		for index := range lengths {
			lengths[index] = index
		}

		return lengths
	}

	lengths := make([]int, 0, most)
	for index := range most / 2 {
		lengths = append(lengths, index, size-1-index)
	}

	return lengths
}

// FuzzDecode checks that Decode and DecodeNext take any input without a
// panic, report only their errors, read a value that delimits itself and
// does not alias the input, and that the value re-encodes canonically and
// renders.
func FuzzDecode(f *testing.F) {
	limitMemory(f)

	for _, seed := range decodeSeeds(f) {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		input := bytes.Clone(data)

		value, size, err := tuple.DecodeNext(input)
		whole, wholeErr := tuple.Decode(data)

		if err != nil {
			require.True(t, errors.Is(err, tuple.ErrTruncated) || errors.Is(err, tuple.ErrInvalid),
				"%v", err)
			require.Error(t, wholeErr)
			require.Equal(t, errors.Is(err, tuple.ErrTruncated),
				errors.Is(wholeErr, tuple.ErrTruncated), "%v, %v", err, wholeErr)

			return
		}

		require.Positive(t, size)
		require.LessOrEqual(t, size, len(data))

		if size == len(data) {
			require.NoError(t, wholeErr)
			requireSame(t, value, whole, "Decode and DecodeNext differ")
		} else {
			require.ErrorIs(t, wholeErr, tuple.ErrTrailing)
		}

		// The value does not alias the input.
		for index := range input {
			input[index] ^= 0xff
		}

		again, _, err := tuple.DecodeNext(data)
		require.NoError(t, err)
		requireSame(t, again, value, "the value aliases the input")

		// The value delimits itself: what follows it is not read.
		head := data[:size:size]

		followed, followedSize, err := tuple.DecodeNext(append(head, 0xc1, 0xdd, 0xff))
		require.NoError(t, err)
		require.Equal(t, size, followedSize)
		requireSame(t, value, followed)

		exact, err := tuple.Decode(head)
		require.NoError(t, err)
		requireSame(t, value, exact)

		for _, length := range prefixLengths(size) {
			_, _, err := tuple.DecodeNext(head[:length])
			require.ErrorIs(t, err, tuple.ErrTruncated, "a prefix of %d of %d bytes", length, size)
		}

		require.LessOrEqual(t, valueDepth(value), decodeDepth)

		// The canonical encoding reads back as the value, and is its own
		// canonical encoding.
		canonical := appendMsgpack(t, nil, value)

		back, err := tuple.Decode(canonical)
		require.NoError(t, err, "%x", canonical)
		requireSame(t, value, back, "%x", canonical)
		require.Equal(t, canonical, appendMsgpack(t, nil, back))

		checkRenderers(t, value)
	})
}

// extHeaderSize is the size of the header AppendExtMsgpack writes for a
// payload of size bytes, its type byte not counted, and its first byte.
func extHeaderSize(size int) (int, byte) {
	switch size {
	case 1:
		return 1, 0xd4
	case 2:
		return 1, 0xd5
	case 4:
		return 1, 0xd6
	case 8:
		return 1, 0xd7
	case 16:
		return 1, 0xd8
	}

	switch {
	case size <= math.MaxUint8:
		return 2, 0xc7
	case size <= math.MaxUint16:
		return 3, 0xc8
	}

	return 5, 0xc9
}

// FuzzAppendExtMsgpack checks that AppendExtMsgpack keeps dst, writes the
// smallest header and a value Decode reads back.
func FuzzAppendExtMsgpack(f *testing.F) {
	for _, seed := range []struct {
		typ  int8
		data []byte
	}{
		{0x42, []byte{7}}, {0, nil}, {-1, []byte{1, 2}}, {-128, make([]byte, 3)},
		{127, make([]byte, 4)}, {tuple.ExtUUID, make([]byte, 16)}, {tuple.ExtUUID, []byte{0}},
		{tuple.ExtDatetime, make([]byte, 8)}, {tuple.ExtDatetime, make([]byte, 16)},
		{tuple.ExtDecimal, []byte{0x02, 0x15, 0x0c}}, {tuple.ExtInterval, []byte{0x00}},
		{tuple.ExtError, make([]byte, 17)}, {5, make([]byte, 255)}, {5, make([]byte, 256)},
		{7, make([]byte, 65535)}, {7, make([]byte, 65536)},
	} {
		f.Add(seed.typ, seed.data, []byte("prefix"))
	}

	f.Fuzz(func(t *testing.T, typ int8, data, prefix []byte) {
		payload := bytes.Clone(data)
		out := tuple.AppendExtMsgpack(bytes.Clone(prefix), typ, payload)

		require.Equal(t, prefix, out[:len(prefix)])

		out = out[len(prefix):]

		headerSize, code := extHeaderSize(len(data))
		require.Len(t, out, headerSize+1+len(data))
		require.Equal(t, code, out[0], "the header of %d bytes", len(data))
		require.Equal(t, byte(typ), out[headerSize]) //nolint:gosec // The byte of the type.
		require.True(t, bytes.Equal(data, out[headerSize+1:]))

		// The output does not alias the payload.
		for index := range payload {
			payload[index] ^= 0xff
		}

		require.True(t, bytes.Equal(data, out[headerSize+1:]))

		value, size, err := tuple.DecodeNext(out)
		require.NoError(t, err)
		require.Len(t, out, size)

		switch typed := value.(type) {
		case tuple.Ext:
			require.Equal(t, typ, typed.Type)
			require.True(t, bytes.Equal(data, typed.Data))
		case uuid.UUID:
			require.Equal(t, tuple.ExtUUID, typ)
			require.Equal(t, data, typed[:])
		default:
			require.Contains(t, []int8{tuple.ExtDecimal, tuple.ExtDatetime, tuple.ExtInterval}, typ,
				describe(value))
			requireSame(t, value, decodeModel(t, value))
		}
	})
}
