package tuple_test

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/tuple"
)

// decodeDepth is the deepest a value Decode returns nests: the items of the
// outermost array or map are at depth 1, and a value at depth 512 is the
// deepest Decode takes.
const decodeDepth = 512

// The MessagePack codes the canonical encoder writes.
const (
	mpFixmap   = 0x80
	mpFixarray = 0x90
	mpFixstr   = 0xa0
	mpNil      = 0xc0
	mpFalse    = 0xc2
	mpTrue     = 0xc3
	mpBin8     = 0xc4
	mpBin16    = 0xc5
	mpBin32    = 0xc6
	mpFloat32  = 0xca
	mpFloat64  = 0xcb
	mpUint8    = 0xcc
	mpUint16   = 0xcd
	mpUint32   = 0xce
	mpUint64   = 0xcf
	mpInt8     = 0xd0
	mpInt16    = 0xd1
	mpInt32    = 0xd2
	mpInt64    = 0xd3
	mpStr8     = 0xd9
	mpStr16    = 0xda
	mpStr32    = 0xdb
	mpArray16  = 0xdc
	mpArray32  = 0xdd
	mpMap16    = 0xde
	mpMap32    = 0xdf

	mpFixintMax    = 0x7f
	mpNegFixintMin = -32
	mpFixstrMax    = 31
	mpFixMax       = 15
)

// The nibbles of the sign of a decimal in its BCD.
const (
	bcdPlus  = 0x0c
	bcdMinus = 0x0d
)

// appendMsgpack appends the canonical MessagePack of a value of the model of
// Decode: the smallest encoding of every integer, length and header, a Map
// in its order with its duplicates, a float32 as MP_FLOAT and a float64 as
// MP_DOUBLE with their bits, a decimal from the digits of its coefficient and
// its exponent - which go-tarantool, trimming its trailing zeros, would not
// keep - a UUID as extension 2, a datetime and an interval as go-tarantool
// encodes their payloads, an Ext as it is.
//
//nolint:cyclop // One case per type is the clearest form.
func appendMsgpack(tb testing.TB, dst []byte, value any) []byte {
	tb.Helper()

	switch typed := value.(type) {
	case nil:
		return append(dst, mpNil)
	case bool:
		if typed {
			return append(dst, mpTrue)
		}

		return append(dst, mpFalse)
	case uint64:
		return appendMsgpackUint(dst, typed)
	case int64:
		return appendMsgpackInt(dst, typed)
	case float32:
		return binary.BigEndian.AppendUint32(append(dst, mpFloat32), math.Float32bits(typed))
	case float64:
		return binary.BigEndian.AppendUint64(append(dst, mpFloat64), math.Float64bits(typed))
	case string:
		return append(appendMsgpackHeader(dst, len(typed), mpFixstr, mpFixstrMax, mpStr8,
			mpStr16, mpStr32), typed...)
	case tuple.Binary:
		return append(appendMsgpackHeader(dst, len(typed), 0, -1, mpBin8, mpBin16, mpBin32),
			typed...)
	case []any:
		dst = appendMsgpackHeader(dst, len(typed), mpFixarray, mpFixMax, 0, mpArray16, mpArray32)
		for _, item := range typed {
			dst = appendMsgpack(tb, dst, item)
		}

		return dst
	case tuple.Map:
		dst = appendMsgpackHeader(dst, len(typed), mpFixmap, mpFixMax, 0, mpMap16, mpMap32)
		for _, item := range typed {
			dst = appendMsgpack(tb, appendMsgpack(tb, dst, item.Key), item.Value)
		}

		return dst
	case decimal.Decimal:
		return tuple.AppendExtMsgpack(dst, tuple.ExtDecimal, decimalPayload(typed))
	case uuid.UUID:
		return tuple.AppendExtMsgpack(dst, tuple.ExtUUID, typed[:])
	case datetime.Datetime:
		raw, err := typed.MarshalMsgpack()
		require.NoError(tb, err)

		return tuple.AppendExtMsgpack(dst, tuple.ExtDatetime, raw)
	case datetime.Interval:
		raw, err := typed.MarshalMsgpack()
		require.NoError(tb, err)

		return tuple.AppendExtMsgpack(dst, tuple.ExtInterval, raw)
	case tuple.Ext:
		return tuple.AppendExtMsgpack(dst, typed.Type, typed.Data)
	}

	tb.Fatalf("appendMsgpack: %T is not a value Decode returns", value)

	return nil
}

func appendMsgpackUint(dst []byte, value uint64) []byte {
	switch {
	case value <= mpFixintMax:
		return append(dst, byte(value))
	case value <= math.MaxUint8:
		return append(dst, mpUint8, byte(value))
	case value <= math.MaxUint16:
		return binary.BigEndian.AppendUint16(append(dst, mpUint16), uint16(value))
	case value <= math.MaxUint32:
		return binary.BigEndian.AppendUint32(append(dst, mpUint32), uint32(value))
	}

	return binary.BigEndian.AppendUint64(append(dst, mpUint64), value)
}

// appendMsgpackInt appends an integer as the number it is: a uint unless it
// is negative.
//
//nolint:gosec // The conversions write the bits of an integer in its range.
func appendMsgpackInt(dst []byte, value int64) []byte {
	switch {
	case value >= 0:
		return appendMsgpackUint(dst, uint64(value))
	case value >= mpNegFixintMin:
		return append(dst, byte(value))
	case value >= math.MinInt8:
		return append(dst, mpInt8, byte(value))
	case value >= math.MinInt16:
		return binary.BigEndian.AppendUint16(append(dst, mpInt16), uint16(value))
	case value >= math.MinInt32:
		return binary.BigEndian.AppendUint32(append(dst, mpInt32), uint32(value))
	}

	return binary.BigEndian.AppendUint64(append(dst, mpInt64), uint64(value))
}

// appendMsgpackHeader appends the smallest header of a string, a binary, an
// array or a map of size items: a fix code if fixMax is not negative, then
// the codes of 8, 16 and 32 bits of length, code8 0 for none.
//
//nolint:gosec // The sizes are checked against the limits of their codes.
func appendMsgpackHeader(dst []byte, size int, fix byte, fixMax int, code8, code16,
	code32 byte,
) []byte {
	switch {
	case size <= fixMax:
		return append(dst, fix|byte(size))
	case code8 != 0 && size <= math.MaxUint8:
		return append(dst, code8, byte(size))
	case size <= math.MaxUint16:
		return binary.BigEndian.AppendUint16(append(dst, code16), uint16(size))
	}

	return binary.BigEndian.AppendUint32(append(dst, code32), uint32(size))
}

// decimalPayload is the payload Tarantool encodes a decimal in: its scale,
// then the digits of its coefficient and its sign in BCD.
func decimalPayload(value decimal.Decimal) []byte {
	coefficient := value.Coefficient()
	digits := coefficient.Abs(coefficient).String()

	nibbles := make([]byte, 0, len(digits)+2)
	if len(digits)%2 == 0 {
		nibbles = append(nibbles, 0)
	}

	for _, digit := range []byte(digits) {
		nibbles = append(nibbles, digit-'0')
	}

	sign := byte(bcdPlus)
	if value.Sign() < 0 {
		sign = bcdMinus
	}

	nibbles = append(nibbles, sign)

	out := appendMsgpackInt(nil, -int64(value.Exponent()))
	for index := 0; index < len(nibbles); index += 2 {
		out = append(out, nibbles[index]<<4|nibbles[index+1])
	}

	return out
}

// decodeModel is value as Decode returns it after its canonical MessagePack.
func decodeModel(tb testing.TB, value any) any {
	tb.Helper()

	raw := appendMsgpack(tb, nil, value)

	back, err := tuple.Decode(raw)
	require.NoError(tb, err, "%x", raw)

	return back
}

// integerValue is an integer of the model of Decode as the number it is.
type integerValue struct {
	negative  bool
	magnitude uint64
}

//nolint:gosec // The magnitude of a negative int64 fits a uint64.
func integerOf(value any) (integerValue, bool) {
	switch typed := value.(type) {
	case uint64:
		return integerValue{negative: false, magnitude: typed}, true
	case int64:
		if typed < 0 {
			return integerValue{negative: true, magnitude: uint64(-(typed + 1)) + 1}, true
		}

		return integerValue{negative: false, magnitude: uint64(typed)}, true
	}

	return integerValue{}, false
}

// sameValue reports whether two values of the model of Decode are the same
// MessagePack value, modulo what the package does not keep: an integer is
// the number it is, whatever its encoding, and a NaN is any NaN. It is
// strict about the rest: the bits of a float, -0 among them, the coefficient
// and the exponent of a decimal, the instant, the offset and the zone of a
// datetime, the order and the duplicates of a map.
//
//nolint:cyclop,gocyclo // One case per type is the clearest form.
func sameValue(left, right any) bool {
	if leftInt, ok := integerOf(left); ok {
		rightInt, ok := integerOf(right)

		return ok && leftInt == rightInt
	}

	switch typed := left.(type) {
	case nil:
		return right == nil
	case bool:
		other, ok := right.(bool)

		return ok && typed == other
	case float64:
		other, ok := right.(float64)

		return ok && sameFloat(typed, other)
	case float32:
		other, ok := right.(float32)

		// A float32 converts to a float64 of its own bits, but for NaN.
		return ok && sameFloat(float64(typed), float64(other))
	case string:
		other, ok := right.(string)

		return ok && typed == other
	case tuple.Binary:
		other, ok := right.(tuple.Binary)

		return ok && bytes.Equal(typed, other)
	case decimal.Decimal:
		other, ok := right.(decimal.Decimal)

		return ok && typed.Exponent() == other.Exponent() &&
			typed.Coefficient().Cmp(other.Coefficient()) == 0
	case uuid.UUID:
		other, ok := right.(uuid.UUID)

		return ok && typed == other
	case datetime.Datetime:
		other, ok := right.(datetime.Datetime)

		return ok && sameDatetime(typed, other)
	case datetime.Interval:
		other, ok := right.(datetime.Interval)

		return ok && typed == other
	case tuple.Ext:
		other, ok := right.(tuple.Ext)

		return ok && typed.Type == other.Type && bytes.Equal(typed.Data, other.Data)
	case []any:
		other, ok := right.([]any)

		return ok && slices.EqualFunc(typed, other, sameValue)
	case tuple.Map:
		other, ok := right.(tuple.Map)

		return ok && slices.EqualFunc(typed, other, func(left, right tuple.MapItem) bool {
			return sameValue(left.Key, right.Key) && sameValue(left.Value, right.Value)
		})
	}

	return false
}

// sameFloat reports whether two floats have the same bits, or are both NaN.
func sameFloat(left, right float64) bool {
	if math.IsNaN(left) || math.IsNaN(right) {
		return math.IsNaN(left) && math.IsNaN(right)
	}

	return math.Float64bits(left) == math.Float64bits(right)
}

// sameDatetime reports whether two datetimes have the same instant, offset
// and zone name.
func sameDatetime(left, right datetime.Datetime) bool {
	leftTime, rightTime := left.ToTime(), right.ToTime()
	_, leftOffset := leftTime.Zone()
	_, rightOffset := rightTime.Zone()

	return leftTime.Equal(rightTime) && leftOffset == rightOffset &&
		leftTime.Location().String() == rightTime.Location().String()
}

// requireSame checks that got is want by sameValue and by the tagged JSON
// assertSameValue compares.
func requireSame(t *testing.T, want, got any, msgAndArgs ...any) {
	t.Helper()

	if !sameValue(want, got) {
		t.Fatalf("%s\nwant %s\n got %s", describeArgs(msgAndArgs), describe(want), describe(got))
	}

	assertSameValue(t, want, got, msgAndArgs...)
}

func describeArgs(msgAndArgs []any) string {
	if len(msgAndArgs) == 0 {
		return ""
	}

	format, ok := msgAndArgs[0].(string)
	if !ok {
		return fmt.Sprint(msgAndArgs...)
	}

	return fmt.Sprintf(format, msgAndArgs[1:]...)
}

// describe is a value for a message: its tagged JSON, cut short.
func describe(value any) string {
	const most = 400

	text, err := tuple.AppendTaggedJSON(nil, value)
	if err != nil {
		text = fmt.Appendf(nil, "%#v", value)
	}

	if len(text) > most {
		text = append(text[:most:most], "..."...)
	}

	return fmt.Sprintf("%T %s", value, text)
}

// valueDepth is the depth of the deepest value in value, 0 for a scalar.
func valueDepth(value any) int {
	deepest := 0

	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			deepest = max(deepest, valueDepth(item)+1)
		}
	case tuple.Map:
		for _, item := range typed {
			deepest = max(deepest, valueDepth(item.Key)+1, valueDepth(item.Value)+1)
		}
	}

	return deepest
}

// limitMemory sets a memory limit for a fuzz target, unless GOMEMLIMIT does.
func limitMemory(f *testing.F) {
	f.Helper()

	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}

	const limit = 2 << 30

	previous := debug.SetMemoryLimit(limit)

	f.Cleanup(func() { debug.SetMemoryLimit(previous) })
}

// fuzzSource reads the choices of a generator from the bytes of a fuzz
// input. Once they run out, every choice is zero.
type fuzzSource struct {
	data []byte
	pos  int
}

func (s *fuzzSource) next() byte {
	if s.pos >= len(s.data) {
		return 0
	}

	char := s.data[s.pos]
	s.pos++

	return char
}

// intn is a choice from 0 to count-1.
func (s *fuzzSource) intn(count int) int {
	if count <= 1 {
		return 0
	}

	if count <= math.MaxUint8+1 {
		return int(s.next()) % count
	}

	return (int(s.next())<<8 | int(s.next())) % count
}

func (s *fuzzSource) uint64() uint64 {
	var raw [8]byte

	for index := range raw {
		raw[index] = s.next()
	}

	return binary.BigEndian.Uint64(raw[:])
}

func (s *fuzzSource) int64() int64 {
	return int64(s.uint64()) //nolint:gosec // The bits of a random integer.
}

// take returns the next count bytes, or as many as are left.
func (s *fuzzSource) take(count int) []byte {
	count = min(count, len(s.data)-s.pos)
	if count <= 0 {
		return []byte{}
	}

	out := bytes.Clone(s.data[s.pos : s.pos+count])

	s.pos += count

	return out
}

func pick[T any](s *fuzzSource, items []T) T {
	return items[s.intn(len(items))]
}

// The tables of values the generator picks from: the edges of the encodings
// and of the texts of numbers.
var (
	uintTable = []uint64{
		0, 1, 5, mpFixintMax, mpFixintMax + 1, math.MaxUint8, math.MaxUint8 + 1, math.MaxUint16,
		math.MaxUint16 + 1, math.MaxUint32, math.MaxUint32 + 1, 1 << 53, 1<<53 + 1,
		math.MaxInt64, 1 << 63, math.MaxUint64, 1000, 1731592956,
	}
	intTable = []int64{
		-1, -5, mpNegFixintMin, mpNegFixintMin - 1, math.MinInt8, math.MinInt8 - 1,
		math.MinInt16, math.MinInt16 - 1, math.MinInt32, math.MinInt32 - 1, -(1 << 53),
		-(1 << 53) - 1, math.MinInt64, math.MinInt64 + 1,
	}
	float64Table = []float64{
		0, math.Copysign(0, -1), math.NaN(), math.Float64frombits(0xfff8000000000001),
		math.Float64frombits(0x7ff0000000000001), math.Inf(1), math.Inf(-1),
		math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64, math.MaxFloat64,
		0x1p-1022, math.Nextafter(0x1p-1022, 0), 1e-6, math.Nextafter(1e-6, 0), 1e-7,
		1e21, math.Nextafter(1e21, 0), 1e20, 0.1, 0.2, 1 << 53, 1<<53 + 2, 1 << 63, 1 << 64,
		-(1 << 63), 1731592956.818, 3, -0.5, 123456789e-15, 5e-324, 1e300, -1e-300,
	}
	float32Table = []float32{
		0, float32(math.Copysign(0, -1)), float32(math.NaN()),
		math.Float32frombits(0xffc00001), float32(math.Inf(1)), float32(math.Inf(-1)),
		math.SmallestNonzeroFloat32, math.MaxFloat32, 1e-6, math.Nextafter32(1e-6, 0), 1e21,
		math.Nextafter32(1e21, 0), 0.1, 1.5, 1 << 24, 1<<24 + 2, 3, -0.5, 1e-45, 3.4e38,
	}
	trickyStrings = []string{
		"", "null", "0", "-0", "007", "t", "NaN", "nan", "true", "false", "yes", "off", "~",
		"1 000", "1,5", "1.5", "12", "+5", "1e3", "Infinity", "-Infinity", "{}", "[1]", `""`,
		`"\"\""`, `"q"`, "$map", "$uuid", "$", "+1 years", "-2 seconds, adjust last",
		"1,0,0,0,0,0,0,0,0", sampleUUID, strings.ToUpper(sampleUUID),
		"2024-11-14T14:02:36Z", "2024-11-14T14:02:36 Europe/Moscow", "2024-11-14",
		"2024-10-27T02:30:00+0100 Europe/Berlin", "2024-11-14T14:02:36A", "\xff", "\xff\xfe",
		"a\xc3", "\xed\xa0\x80", "�", "é", "\u0085", "\r", "a\nb", "a \n b", "\xc0\x80",
		"\xe2\x80\xa8", "\x00", "\x7f", "\n", "it's\n", "\\",
		"'", " lead", "trail ", "\t", "<&>", "ext:66:Bw==", "\xef\xbb\xbf", "null\x00", "\r\n",
		"a: b", "- x", "#", "&a", "*a", "!x", "%", "@", "`", "?", "|", ">", "0x1F", "0o17",
		".inf", "-.inf", ".nan", "1_000", "+.5", "Yes", "ON", "y", "N", "1:20", "2001-12-14",
	}
	decimalExponents = []int64{
		0, -1, -2, -3, -6, -7, -10, 1, 2, 3, 6, 38, -38, 999_999, -999_999, -1_000_000,
		-1_000_075, -1_000_076, 1_000_000, -40, 76, -76, math.MaxInt32, math.MinInt32 + 1,
	}
	// datetimeSeconds are instants around the transitions of summer time
	// of Berlin and New York in 2024, and the edges of the range of a
	// datetime.
	datetimeSeconds = []int64{
		0, 1729989000, 1729992600, 1729990800, 1711846800, 1711850400, 1730611800,
		1730615400, 1710054000, 1710057600, -62135596800, 253402300799, -185604722870400,
		185480451417600, 1731592956, -5000000000, 1293703200,
	}
	datetimeNanos = []int64{0, 1, 5, 27_000_000, 120_000, 999_999_999, 1_000_000, 500_000_000}
	// datetimeOffsets are offsets in minutes.
	datetimeOffsets = []int{0, 60, -90, 180, 840, -720, 345, -1, 1, 120, -300, 330, 1439}
	// datetimeZones are zones of the time zone database: with summer time,
	// with offsets of a half or a quarter of an hour, with a day skipped.
	datetimeZones = []string{
		"Europe/Berlin", "America/New_York", "Europe/Moscow", "Asia/Kolkata",
		"Australia/Lord_Howe", "Pacific/Apia", "Pacific/Chatham", "America/Sao_Paulo",
		"Asia/Kathmandu", "UTC", "Etc/GMT+12", "Africa/Casablanca", "Europe/Dublin",
		"Antarctica/Troll", "Pacific/Kiritimati",
	}
	// zoneAbbreviations are abbreviations Tarantool takes for a fixed
	// offset, in minutes, military one-letter zones among them.
	zoneAbbreviations = []struct {
		name   string
		offset int
	}{
		{"MSK", 180}, {"CET", 60}, {"EST", -300}, {"Z", 0}, {"A", 60}, {"N", -60},
		{"M", 720}, {"Y", -720}, {"UTC", 0}, {"MSD", 240},
	}
	extTypes = []int8{3, 5, 7, 0x42, 0, -1, -128, 127, 1, 2, 4, 6}
	tagKeys  = []string{"$map", "$uuid", "$x", "$", "$binary", "$ext", "$decimal"}
)

// generator builds a value of the model of Decode from the choices of a
// fuzz input.
type generator struct {
	tb     testing.TB
	src    *fuzzSource
	budget int
}

// The kinds of value the generator builds.
const (
	kindNil = iota
	kindBool
	kindUintTable
	kindUintRaw
	kindIntTable
	kindIntRaw
	kindFloat64Table
	kindFloat64Raw
	kindFloat32Table
	kindFloat32Raw
	kindStringTable
	kindStringRaw
	kindBinary
	kindDecimal
	kindUUID
	kindDatetime
	kindInterval
	kindExt
	kindArray
	kindMap
	kindChain
	kindCount
)

// kindScalars is the number of kinds of a value that holds no other.
const kindScalars = kindArray

// genValue builds a value from data and returns it as Decode returns it.
func genValue(tb testing.TB, data []byte) any {
	tb.Helper()

	gen := generator{tb: tb, src: &fuzzSource{data: data, pos: 0}, budget: 64}

	return decodeModel(tb, gen.value(0))
}

// value builds a value at depth, the depth Decode finds it at.
//
//nolint:cyclop // One case per kind is the clearest form.
func (g *generator) value(depth int) any {
	kinds := kindCount
	if depth >= decodeDepth || g.budget <= 0 {
		kinds = kindScalars
	}

	src := g.src

	switch src.intn(kinds) {
	case kindNil:
		return nil
	case kindBool:
		return src.next()&1 == 1
	case kindUintTable:
		return pick(src, uintTable)
	case kindUintRaw:
		return src.uint64()
	case kindIntTable:
		return pick(src, intTable)
	case kindIntRaw:
		return src.int64()
	case kindFloat64Table:
		return pick(src, float64Table)
	case kindFloat64Raw:
		return math.Float64frombits(src.uint64())
	case kindFloat32Table:
		return pick(src, float32Table)
	case kindFloat32Raw:
		return math.Float32frombits(uint32(src.uint64())) //nolint:gosec // Random bits.
	case kindStringTable:
		return pick(src, trickyStrings)
	case kindStringRaw:
		return string(src.take(src.intn(64)))
	case kindBinary:
		return tuple.Binary(src.take(src.intn(64)))
	case kindDecimal:
		return g.decimal()
	case kindUUID:
		var id uuid.UUID

		copy(id[:], src.take(len(id)))

		return id
	case kindDatetime:
		return g.datetime()
	case kindInterval:
		return g.interval()
	case kindExt:
		return g.ext()
	case kindArray:
		return g.array(depth)
	case kindMap:
		return g.mapOf(depth)
	}

	return g.chain(depth)
}

// decimal builds a decimal of up to 40 digits at an exponent of the table,
// or an Ext if Tarantool holds no such decimal.
func (g *generator) decimal() any {
	src := g.src

	digits := make([]byte, src.intn(41))
	for index := range digits {
		digits[index] = "0123456789"[src.intn(10)]
	}

	if len(digits) == 0 {
		digits = []byte{'0'}
	}

	exponent := pick(src, decimalExponents)
	if src.next()&1 == 1 {
		exponent += int64(int8(src.next())) //nolint:gosec // A small random shift.
	}

	sign := ""
	if src.next()&1 == 1 {
		sign = "-"
	}

	value, err := decimal.NewDecimalFromString(sign + string(digits) + "E" +
		strconv.FormatInt(exponent, 10))
	if err != nil {
		return string(digits)
	}

	return value
}

// datetimeLocation picks the location of a datetime: an offset with no zone,
// a zone of the database at its own offset or at another one, as Decode keeps
// the offset of a payload, or an abbreviation at its offset or another one.
func datetimeLocation(src *fuzzSource) *time.Location {
	offset := pick(src, datetimeOffsets)
	if src.next()&1 == 1 {
		offset = src.intn(14*60+12*60+1) - 12*60
	}

	switch src.intn(5) {
	case 0:
		return time.FixedZone(datetime.NoTimezone, offset*60)
	case 1, 2:
		name := pick(src, datetimeZones)

		loc, err := time.LoadLocation(name)
		if err != nil {
			return time.FixedZone(datetime.NoTimezone, offset*60)
		}

		if src.next()&3 == 0 {
			return time.FixedZone(name, offset*60)
		}

		return loc
	case 3:
		abbrev := pick(src, zoneAbbreviations)

		return time.FixedZone(abbrev.name, abbrev.offset*60)
	}

	return time.FixedZone(pick(src, zoneAbbreviations).name, offset*60)
}

func (g *generator) datetime() any {
	src := g.src

	seconds := pick(src, datetimeSeconds)

	switch src.intn(4) {
	case 0:
		seconds += int64(src.intn(7200)) - 3600
	case 1:
		seconds = src.int64() % 185480451417600
	}

	nanos := pick(src, datetimeNanos)
	if src.next()&1 == 1 {
		nanos = int64(src.uint64() % 1_000_000_000)
	}

	value, err := datetime.NewDatetime(time.Unix(seconds, nanos).In(datetimeLocation(src)))
	if err != nil {
		return tuple.Ext{Type: tuple.ExtDatetime, Data: src.take(16)}
	}

	return value
}

// intervalUnit picks the value of a unit of an interval.
func intervalUnit(src *fuzzSource) int64 {
	switch src.intn(6) {
	case 0, 1, 2:
		return 0
	case 3:
		return pick(src, []int64{
			1, -1, math.MaxInt64, math.MinInt64, 7, -7, 1_000_000_000, 12, -12, 60, 24,
		})
	case 4:
		return int64(int8(src.next())) //nolint:gosec // A small random value.
	}

	return src.int64()
}

func (g *generator) interval() any {
	src := g.src

	value := datetime.Interval{
		Year: intervalUnit(src), Month: intervalUnit(src), Week: intervalUnit(src),
		Day: intervalUnit(src), Hour: intervalUnit(src), Min: intervalUnit(src),
		Sec: intervalUnit(src), Nsec: intervalUnit(src), Adjust: datetime.NoneAdjust,
	}

	value.Adjust = datetime.Adjust(src.intn(3))
	if src.intn(16) == 0 {
		value.Adjust = datetime.Adjust(3 + src.intn(100))
	}

	return value
}

// ext builds an extension of a type of the table with any payload, which
// Decode then takes for the value of a type it knows if it decodes.
func (g *generator) ext() any {
	src := g.src

	typ := pick(src, extTypes)
	if src.intn(8) == 0 {
		typ = int8(src.next()) //nolint:gosec // A random type.
	}

	size := src.intn(40)
	if src.next()&1 == 1 {
		size = pick(src, []int{0, 1, 2, 3, 4, 8, 16, 17})
	}

	return tuple.Ext{Type: typ, Data: src.take(size)}
}

func (g *generator) array(depth int) any {
	count := g.src.intn(6)
	if g.src.intn(8) == 0 {
		count = g.src.intn(40)
	}

	items := make([]any, 0, count)

	for range count {
		g.budget--

		items = append(items, g.value(depth+1))
	}

	return items
}

// mapOf builds a map of keys of any kind, a first key that looks like a tag
// and duplicate keys among them.
func (g *generator) mapOf(depth int) any {
	src := g.src
	count := src.intn(6)
	items := make(tuple.Map, 0, count)

	for index := range count {
		g.budget--

		var key any

		switch {
		case index == 0 && src.intn(4) == 0:
			key = pick(src, tagKeys)
		case index > 0 && src.intn(5) == 0:
			key = items[src.intn(index)].Key
		case src.next()&1 == 0:
			key = pick(src, trickyStrings)
		default:
			key = g.value(depth + 1)
		}

		items = append(items, tuple.MapItem{Key: key, Value: g.value(depth + 1)})
	}

	return items
}

// chain builds a chain of arrays or maps of one item down to the depth
// Decode takes, or near it.
func (g *generator) chain(depth int) any {
	src := g.src
	room := decodeDepth - depth

	length := max(0, min(room, pick(src, []int{room, room, room - 1, 200, 199, 50,
		src.intn(room + 1)})))
	style := src.intn(4)

	g.budget -= 8

	leaf := g.value(decodeDepth)

	for range length {
		switch style {
		case 0:
			leaf = []any{leaf}
		case 1:
			leaf = tuple.Map{{Key: "k", Value: leaf}}
		case 2:
			leaf = tuple.Map{{Key: uint64(0), Value: leaf}}
		default:
			leaf = tuple.Map{{Key: "$a", Value: leaf}, {Key: "b", Value: nil}}
		}
	}

	return leaf
}

// genForeign builds a value of the types go-tarantool decodes a response
// into: integers and floats of any width, []byte, Go maps, pointers.
//
//nolint:cyclop // One case per kind is the clearest form.
func genForeign(tb testing.TB, src *fuzzSource, depth int) any {
	tb.Helper()

	const foreignKinds = 18

	kinds := foreignKinds
	if depth > 3 {
		kinds = 13
	}

	raw := src.int64()

	//nolint:gosec // The conversions take the bits of a random integer.
	switch src.intn(kinds) {
	case 0:
		return int(raw)
	case 1:
		return int8(raw)
	case 2:
		return int16(raw)
	case 3:
		return int32(raw)
	case 4:
		return uint(raw)
	case 5:
		return uint8(raw)
	case 6:
		return uint16(raw)
	case 7:
		return uint32(raw)
	case 8:
		return src.take(src.intn(8))
	case 9:
		gen := generator{tb: tb, src: src, budget: 4}

		return decodeModel(tb, gen.value(decodeDepth))
	case 10:
		if src.next()&1 == 0 {
			return (*decimal.Decimal)(nil)
		}

		value := decimal.MustNewDecimal(strconv.FormatInt(raw, 10) + "E-3")

		return &value
	case 11:
		if src.next()&1 == 0 {
			return (*datetime.Datetime)(nil)
		}

		value, err := datetime.NewDatetime(time.Unix(raw%1e10, 0).In(datetimeLocation(src)))
		if err != nil {
			return (*datetime.Datetime)(nil)
		}

		return &value
	case 12:
		if src.next()&1 == 0 {
			return (*datetime.Interval)(nil)
		}

		return &datetime.Interval{Year: raw % 100, Sec: int64(int8(raw)), Adjust: 2}
	case 13, 14:
		items := make([]any, src.intn(4))
		for index := range items {
			items[index] = genForeign(tb, src, depth+1)
		}

		return items
	case 15, 16:
		items := make(map[any]any)
		for range src.intn(5) {
			items[foreignKey(src)] = genForeign(tb, src, depth+1)
		}

		return items
	}

	items := make(map[string]any)
	for range src.intn(5) {
		items[pick(src, trickyStrings)] = genForeign(tb, src, depth+1)
	}

	return items
}

// foreignKey builds a key of a Go map: a comparable value of any width.
//
//nolint:gosec // The conversions take the bits of a random integer.
func foreignKey(src *fuzzSource) any {
	raw := src.int64() % 4

	switch src.intn(10) {
	case 0:
		return int8(raw)
	case 1:
		return uint64(raw)
	case 2:
		return int(raw)
	case 3:
		return float64(raw)
	case 4:
		return math.NaN()
	case 5:
		return nil
	case 6:
		return raw > 0
	case 7:
		return uuid.UUID{}
	case 8:
		return strconv.FormatInt(raw, 10)
	}

	return pick(src, trickyStrings)
}

// normalizeForeign is a value of the types go-tarantool decodes into as the
// renderers take it: an integer of any width as Decode returns one, a []byte
// a Binary, a pointer what it points to, and a Go map a Map in the order the
// package documents - by the text of the keys, then by the Go types of the
// keys, then by the tagged JSON of the entries.
//
//nolint:cyclop // One case per type is the clearest form.
func normalizeForeign(value any) any {
	switch typed := value.(type) {
	case int:
		return normalizeInt(int64(typed))
	case int8:
		return normalizeInt(int64(typed))
	case int16:
		return normalizeInt(int64(typed))
	case int32:
		return normalizeInt(int64(typed))
	case int64:
		return normalizeInt(typed)
	case uint:
		return uint64(typed)
	case uint8:
		return uint64(typed)
	case uint16:
		return uint64(typed)
	case uint32:
		return uint64(typed)
	case []byte:
		return tuple.Binary(typed)
	case *decimal.Decimal:
		if typed == nil {
			return nil
		}

		return *typed
	case *datetime.Datetime:
		if typed == nil {
			return nil
		}

		return *typed
	case *datetime.Interval:
		if typed == nil {
			return nil
		}

		return *typed
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = normalizeForeign(item)
		}

		return items
	case map[any]any:
		items := make(tuple.Map, 0, len(typed))
		for key, item := range typed {
			items = append(items, tuple.MapItem{Key: key, Value: item})
		}

		return normalizeItems(items)
	case map[string]any:
		items := make(tuple.Map, 0, len(typed))
		for key, item := range typed {
			items = append(items, tuple.MapItem{Key: key, Value: item})
		}

		return normalizeItems(items)
	}

	return value
}

func normalizeInt(value int64) any {
	if value >= 0 {
		return uint64(value)
	}

	return value
}

// normalizeItems sorts the entries of a Go map as the package documents and
// normalizes them.
func normalizeItems(items tuple.Map) tuple.Map {
	slices.SortStableFunc(items, func(left, right tuple.MapItem) int {
		order := strings.Compare(keyText(left.Key), keyText(right.Key))
		if order != 0 {
			return order
		}

		order = strings.Compare(fmt.Sprintf("%T", left.Key), fmt.Sprintf("%T", right.Key))
		if order != 0 {
			return order
		}

		return strings.Compare(entryText(left), entryText(right))
	})

	out := make(tuple.Map, len(items))
	for index, item := range items {
		out[index] = tuple.MapItem{
			Key: normalizeForeign(item.Key), Value: normalizeForeign(item.Value),
		}
	}

	return out
}

// keyText is the text of a map key in JSON: its Text, or its JSON.
func keyText(key any) string {
	if text, ok := tuple.Text(key); ok {
		return text
	}

	encoded, err := tuple.AppendJSON(nil, key)
	if err != nil {
		return fmt.Sprint(key)
	}

	return string(encoded)
}

func entryText(item tuple.MapItem) string {
	text, _ := tuple.AppendTaggedJSON(nil, []any{item.Key, item.Value})

	return string(text)
}

// displayString is text with each byte that is not part of UTF-8 replaced by
// U+FFFD, as the display formats write it.
func displayString(text string) string {
	if utf8.ValidString(text) {
		return text
	}

	out := make([]byte, 0, len(text)+8)

	for pos := 0; pos < len(text); {
		symbol, size := utf8.DecodeRuneInString(text[pos:])

		out = utf8.AppendRune(out, symbol)

		pos += size
	}

	return string(out)
}

// hasText reports whether a value of the model of Decode has a Text: any
// but nil, an array and a map.
func hasText(value any) bool {
	switch value.(type) {
	case nil, []any, tuple.Map:
		return false
	}

	return true
}

// knownBugs are the bugs the fuzz targets found that a value still hits,
// each named after its item in the report of the fuzzing: a check of a
// contract a known bug breaks is skipped for such a value, so that the
// fuzzer goes on to find others.
var knownBugs = []struct {
	name string
	hits func(value any) bool
}{}

// knownBug is the name of the known bug value hits, or "".
func knownBug(value any) string {
	for _, bug := range knownBugs {
		if bug.hits(value) {
			return bug.name
		}
	}

	return ""
}

// skipKnownBugs skips a fuzz input whose value hits a known bug.
func skipKnownBugs(t *testing.T, value any) {
	t.Helper()

	if name := knownBug(value); name != "" {
		t.Skip(name)
	}
}

// fieldHolds reports whether a field holds a value as holds does, a field
// with a default taking nil as well.
func fieldHolds(field tuple.Field, value any) bool {
	if value == nil && (field.Default != nil || field.DefaultFunc != "") {
		return true
	}

	return holds(field, value)
}

// checkRenderers checks every renderer on a value of the model of Decode.
func checkRenderers(t *testing.T, value any) {
	t.Helper()

	_, ok := tuple.Text(value)
	require.Equal(t, hasText(value), ok, "Text of %s", describe(value))

	checkJSON(t, value)
	checkYAML(t, value)
	checkLua(t, value)
	checkTaggedJSON(t, value)
}

// checkJSON checks that AppendJSON writes valid JSON of valid UTF-8, the
// same every time and as encoding/json does through Value, of the display
// mapping of the package.
func checkJSON(t *testing.T, value any) {
	t.Helper()

	prefix := []byte("prefix")

	out, err := tuple.AppendJSON(bytes.Clone(prefix), value)
	require.NoError(t, err, describe(value))
	require.Equal(t, prefix, out[:len(prefix)])

	out = out[len(prefix):]
	require.True(t, json.Valid(out), "%s: %q", describe(value), out)
	require.True(t, utf8.Valid(out), "%s: %q", describe(value), out)

	again, err := tuple.AppendJSON(nil, value)
	require.NoError(t, err)
	require.Equal(t, string(out), string(again), "AppendJSON is not deterministic")

	marshaled, err := json.Marshal(tuple.Value{V: value})
	require.NoError(t, err, describe(value))
	require.Equal(t, jsonTokens(t, out), jsonTokens(t, marshaled), describe(value))

	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	expectJSON(t, dec, value)
}

// jsonTokens is the stream of the tokens of a JSON text.
func jsonTokens(t *testing.T, text []byte) []any {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()

	var tokens []any

	for {
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return tokens
		}

		require.NoError(t, err, "%q", text)

		tokens = append(tokens, token)
	}
}

// expectJSON reads the tokens of value from dec, as AppendJSON writes it.
//
//nolint:cyclop // One case per type is the clearest form.
func expectJSON(t *testing.T, dec *json.Decoder, value any) {
	t.Helper()

	token, err := dec.Token()
	require.NoError(t, err)

	switch typed := value.(type) {
	case nil:
		require.Nil(t, token)
	case bool:
		require.Equal(t, typed, token)
	case uint64, int64:
		text, _ := tuple.Text(value)
		require.Equal(t, json.Number(text), token)
	case float64:
		expectJSONFloat(t, token, typed, 64)
	case float32:
		expectJSONFloat(t, token, float64(typed), 32)
	case string:
		require.Equal(t, displayString(typed), token)
	case []any:
		require.Equal(t, json.Delim('['), token)

		for _, item := range typed {
			expectJSON(t, dec, item)
		}

		token, err = dec.Token()
		require.NoError(t, err)
		require.Equal(t, json.Delim(']'), token)
	case tuple.Map:
		require.Equal(t, json.Delim('{'), token)

		for _, item := range typed {
			key, err := dec.Token()
			require.NoError(t, err)
			require.Equal(t, displayString(keyText(item.Key)), key)
			expectJSON(t, dec, item.Value)
		}

		token, err = dec.Token()
		require.NoError(t, err)
		require.Equal(t, json.Delim('}'), token)
	default:
		text, _ := tuple.Text(value)
		require.Equal(t, displayString(text), token, describe(value))
	}
}

func expectJSONFloat(t *testing.T, token any, number float64, bitSize int) {
	t.Helper()

	if math.IsNaN(number) || math.IsInf(number, 0) {
		text, _ := tuple.Text(number)
		require.Equal(t, text, token)

		return
	}

	text, ok := token.(json.Number)
	require.True(t, ok, "%v is not a number", token)

	back, err := strconv.ParseFloat(string(text), bitSize)
	require.NoError(t, err)
	require.Equal(t, math.Float64bits(number), math.Float64bits(back), string(text))
}

// checkYAML checks that the YAML of a value marshals, reads back and holds
// the value as YAMLNode documents it.
func checkYAML(t *testing.T, value any) {
	t.Helper()

	out, err := yaml.Marshal(tuple.Value{V: value})
	require.NoError(t, err, describe(value))

	var doc yaml.Node

	require.NoError(t, yaml.Unmarshal(out, &doc), "%s: %q", describe(value), out)
	require.Equal(t, yaml.DocumentNode, doc.Kind)
	require.Len(t, doc.Content, 1)

	expectYAML(t, doc.Content[0], value, out)
}

// yaml11Words are the words YAML 1.1 reads as a bool, which YAMLNode quotes.
var yaml11Words = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true,
	"n": true, "N": true, "no": true, "No": true, "NO": true,
	"on": true, "On": true, "ON": true, "off": true, "Off": true, "OFF": true,
}

// expectYAML checks that a node read back from YAML holds value.
//
//nolint:cyclop // One case per type is the clearest form.
func expectYAML(t *testing.T, node *yaml.Node, value any, out []byte) {
	t.Helper()

	msg := fmt.Sprintf("%s in %q", describe(value), out)

	switch typed := value.(type) {
	case []any:
		require.Equal(t, yaml.SequenceNode, node.Kind, msg)
		require.Len(t, node.Content, len(typed), msg)

		for index, item := range typed {
			expectYAML(t, node.Content[index], item, out)
		}

		return
	case tuple.Map:
		require.Equal(t, yaml.MappingNode, node.Kind, msg)
		require.Len(t, node.Content, 2*len(typed), msg)

		for index, item := range typed {
			expectYAML(t, node.Content[2*index], item.Key, out)
			expectYAML(t, node.Content[2*index+1], item.Value, out)
		}

		return
	}

	require.Equal(t, yaml.ScalarNode, node.Kind, msg)

	switch typed := value.(type) {
	case nil:
		require.Equal(t, "!!null", node.ShortTag(), msg)
	case bool:
		require.Equal(t, "!!bool", node.ShortTag(), msg)
		require.Equal(t, strconv.FormatBool(typed), node.Value, msg)
	case uint64, int64:
		require.Equal(t, "!!int", node.ShortTag(), msg)

		text, _ := tuple.Text(value)
		require.Equal(t, text, node.Value, msg)
	case float64:
		expectYAMLFloat(t, node, typed, 64, msg)
	case float32:
		expectYAMLFloat(t, node, float64(typed), 32, msg)
	case string:
		require.Equal(t, "!!str", node.ShortTag(), msg)
		require.Equal(t, displayString(typed), node.Value, msg)

		if yaml11Words[typed] {
			require.NotZero(t, node.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle), msg)
		}
	case tuple.Binary:
		require.Equal(t, "!!binary", node.ShortTag(), msg)

		data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(node.Value), ""))
		require.NoError(t, err, msg)
		require.Equal(t, []byte(typed), data, msg)
	default:
		require.Equal(t, "!!str", node.ShortTag(), msg)

		text, _ := tuple.Text(value)
		require.Equal(t, text, node.Value, msg)
	}
}

func expectYAMLFloat(t *testing.T, node *yaml.Node, number float64, bitSize int, msg string) {
	t.Helper()

	require.Equal(t, "!!float", node.ShortTag(), msg)

	switch {
	case math.IsNaN(number):
		require.Equal(t, ".nan", node.Value, msg)
	case math.IsInf(number, 1):
		require.Equal(t, ".inf", node.Value, msg)
	case math.IsInf(number, -1):
		require.Equal(t, "-.inf", node.Value, msg)
	default:
		back, err := strconv.ParseFloat(node.Value, bitSize)
		require.NoError(t, err, msg)
		require.Equal(t, math.Float64bits(number), math.Float64bits(back), msg)
	}
}

// checkLua checks that the Lua of a value is printable ASCII, or the UTF-8
// of a string literal, with its brackets balanced outside its literals.
func checkLua(t *testing.T, value any) {
	t.Helper()

	prefix := []byte("x = ")
	out := tuple.AppendLua(bytes.Clone(prefix), value)
	require.Equal(t, prefix, out[:len(prefix)])

	out = out[len(prefix):]
	require.Equal(t, string(out), string(tuple.AppendLua(nil, value)),
		"AppendLua is not deterministic")
	require.NoError(t, luaBalanced(out), "%s: %q", describe(value), out)

	if text, ok := value.(string); ok {
		back, err := unquoteLua(string(out))
		require.NoError(t, err, "%q", out)
		require.Equal(t, text, back)
	}
}

var (
	errLuaBracket = errors.New("unbalanced bracket")
	errLuaByte    = errors.New("a byte that is not printable ASCII outside a literal")
	errLuaString  = errors.New("a malformed string literal")
)

// luaBalanced checks the brackets of a Lua expression outside its string
// literals, which are single-quoted with backslash escapes.
func luaBalanced(code []byte) error {
	closing := map[byte]byte{')': '(', ']': '[', '}': '{'}

	var stack []byte

	for pos := 0; pos < len(code); pos++ {
		char := code[pos]

		switch {
		case char == '\'':
			end, err := luaStringEnd(code, pos)
			if err != nil {
				return err
			}

			pos = end
		case char == '(' || char == '[' || char == '{':
			stack = append(stack, char)
		case closing[char] != 0:
			if len(stack) == 0 || stack[len(stack)-1] != closing[char] {
				return fmt.Errorf("%w %q at %d", errLuaBracket, char, pos)
			}

			stack = stack[:len(stack)-1]
		case char < ' ' || char >= utf8.RuneSelf || char == '"':
			return fmt.Errorf("%w: %q at %d", errLuaByte, char, pos)
		}
	}

	if len(stack) > 0 {
		return fmt.Errorf("%w: %q left open", errLuaBracket, stack)
	}

	return nil
}

// luaStringEnd is the position of the quote that ends the string literal
// that starts at start.
func luaStringEnd(code []byte, start int) (int, error) {
	for pos := start + 1; pos < len(code); pos++ {
		switch code[pos] {
		case '\\':
			pos++
		case '\'':
			return pos, nil
		case '\n', '\r':
			return 0, fmt.Errorf("%w: a line break at %d", errLuaString, pos)
		}
	}

	return 0, fmt.Errorf("%w: no end", errLuaString)
}

// unquoteLua reads a single-quoted Lua string literal with the escapes
// AppendLuaString writes.
func unquoteLua(literal string) (string, error) {
	if len(literal) < 2 || literal[0] != '\'' || literal[len(literal)-1] != '\'' {
		return "", fmt.Errorf("%w: %q is not quoted", errLuaString, literal)
	}

	body := literal[1 : len(literal)-1]
	out := make([]byte, 0, len(body))

	for pos := 0; pos < len(body); pos++ {
		char := body[pos]
		if char == '\'' {
			return "", fmt.Errorf("%w: a bare quote at %d", errLuaString, pos)
		}

		if char != '\\' {
			out = append(out, char)

			continue
		}

		pos++
		if pos >= len(body) {
			return "", fmt.Errorf("%w: a backslash at the end", errLuaString)
		}

		switch body[pos] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case '\\', '\'':
			out = append(out, body[pos])
		case 'x':
			if pos+3 > len(body) {
				return "", fmt.Errorf("%w: a short \\x", errLuaString)
			}

			code, err := strconv.ParseUint(body[pos+1:pos+3], 16, 8)
			if err != nil {
				return "", fmt.Errorf("%w: a bad \\x at %d", errLuaString, pos)
			}

			out = append(out, byte(code))

			pos += 2
		default:
			return "", fmt.Errorf("%w: an escape %q", errLuaString, body[pos])
		}
	}

	return string(out), nil
}

// checkTaggedJSON checks that the tagged JSON of a value is valid JSON, the
// same every time, and reads back as the value.
func checkTaggedJSON(t *testing.T, value any) {
	t.Helper()

	prefix := []byte("[")

	out, err := tuple.AppendTaggedJSON(bytes.Clone(prefix), value)
	require.NoError(t, err, describe(value))
	require.Equal(t, prefix, out[:len(prefix)])

	out = out[len(prefix):]
	require.True(t, json.Valid(out), "%s: %q", describe(value), out)

	again, err := tuple.AppendTaggedJSON(nil, value)
	require.NoError(t, err)
	require.Equal(t, string(out), string(again), "AppendTaggedJSON is not deterministic")

	if knownBug(value) != "" {
		return
	}

	back, err := tuple.ParseTaggedJSON(out)
	require.NoError(t, err, "%q", out)
	requireSame(t, value, back, "%q", out)
}

// luaIntegers finds the 64-bit integer literals of LuaJIT.
var luaIntegers = regexp.MustCompile(`(\d)U?LL\b`)

// checkForeign checks that a value of the types go-tarantool decodes into
// renders as its normalized form.
func checkForeign(t *testing.T, value, normal any) {
	t.Helper()

	msg := describe(normal)

	text, ok := tuple.Text(value)
	normalText, normalOK := tuple.Text(normal)
	require.Equal(t, normalOK, ok, msg)
	require.Equal(t, normalText, text, msg)

	jsonOut, err := tuple.AppendJSON(nil, value)
	require.NoError(t, err, msg)

	normalJSON, err := tuple.AppendJSON(nil, normal)
	require.NoError(t, err, msg)
	//nolint:testifylint // The bytes count, not the JSON value: the order of a map.
	require.Equal(t, string(normalJSON), string(jsonOut), msg)

	yamlOut, err := yaml.Marshal(tuple.Value{V: value})
	require.NoError(t, err, msg)

	normalYAML, err := yaml.Marshal(tuple.Value{V: normal})
	require.NoError(t, err, msg)
	//nolint:testifylint // The bytes count, not the YAML value: the order of a map.
	require.Equal(t, string(normalYAML), string(yamlOut), msg)

	// A large positive int is an int64 of LuaJIT, its uint64 one of uint64:
	// Tarantool encodes both as the same MP_UINT.
	require.Equal(t, luaIntegers.ReplaceAllString(string(tuple.AppendLua(nil, normal)), "$1"),
		luaIntegers.ReplaceAllString(string(tuple.AppendLua(nil, value)), "$1"), msg)

	tagged, err := tuple.AppendTaggedJSON(nil, value)
	require.NoError(t, err, msg)

	normalTagged, err := tuple.AppendTaggedJSON(nil, normal)
	require.NoError(t, err, msg)
	require.Equal(t, string(normalTagged), string(tagged), msg)

	for _, typ := range []tuple.FieldType{tuple.FieldAny, tuple.FieldScalar, tuple.FieldInteger} {
		field := tuple.Field{Name: "f", Type: typ, IsNullable: false}
		got, gotErr := tuple.AppendField(nil, value, field, tuple.FieldOptions{})
		want, wantErr := tuple.AppendField(nil, normal, field, tuple.FieldOptions{})

		require.Equal(t, wantErr == nil, gotErr == nil, "%s field: %s: %v, %v", typ, msg, wantErr,
			gotErr)
		require.Equal(t, string(want), string(got), "%s field: %s", typ, msg)
	}
}

// seedValues are values of every kind, as Decode returns them, for the seed
// corpora.
func seedValues(tb testing.TB) []any {
	tb.Helper()

	values := []any{
		nil, true, false, uint64(0), uint64(math.MaxUint64), int64(-1), int64(math.MinInt64),
		math.Copysign(0, -1), math.NaN(), 1e21, float32(0.1), "", "it's\n", "\xff",
		tuple.Binary{0, 1, 0xff}, decimal.MustNewDecimal("-12.340"),
		decimal.MustNewDecimal("1E+999999"), uuid.MustParse(sampleUUID),
		datetime.Interval{Year: 1, Sec: -2, Adjust: datetime.LastAdjust},
		tuple.Ext{Type: 0x42, Data: []byte{7}}, tuple.Ext{Type: tuple.ExtUUID, Data: []byte{0}},
		[]any{}, tuple.Map{}, tuple.Map{{Key: "$uuid", Value: "x"}},
		tuple.Map{{Key: uint64(1), Value: "a"}, {Key: uint64(1), Value: "b"}},
		tuple.Map{{Key: nil, Value: []any{}}, {Key: 1.5, Value: tuple.Binary("x")}},
	}

	moments := []time.Time{
		time.Unix(1729989000, 0), time.Unix(1729992600, 27_000_000),
		time.Unix(1730611800, 5), time.Unix(-62135596800, 0),
	}
	locations := []*time.Location{
		loadLocation(tb, "Europe/Berlin"), loadLocation(tb, "America/New_York"),
		time.FixedZone("Z", 0), time.FixedZone("MSK", 3*3600),
		time.FixedZone(datetime.NoTimezone, -90*60), time.FixedZone("Europe/Berlin", 0),
	}

	out := make([]any, 0, len(values)+len(moments)*len(locations))
	for _, value := range values {
		out = append(out, decodeModel(tb, value))
	}

	for _, moment := range moments {
		for _, loc := range locations {
			value, err := datetime.NewDatetime(moment.In(loc))
			require.NoError(tb, err)

			out = append(out, decodeModel(tb, value))
		}
	}

	return out
}

func loadLocation(tb testing.TB, name string) *time.Location {
	tb.Helper()

	loc, err := time.LoadLocation(name)
	if err != nil {
		tb.Skipf("no time zone %s: %v", name, err)
	}

	return loc
}

// generatorSeeds are fuzz inputs that make the generator build each kind of
// value, and a few longer ones.
func generatorSeeds() [][]byte {
	seeds := make([][]byte, 0, kindCount*2+4)

	for kind := range byte(kindCount) {
		seeds = append(seeds, []byte{kind, 1, 2, 3, 4, 5, 6, 7, 8},
			[]byte{kind, 0xff, 0x80, 0x7f, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60})
	}

	return append(seeds,
		[]byte{kindArray, 3, kindStringTable, 7, kindFloat64Table, 1, kindDecimal, 5, 1, 2},
		[]byte{kindMap, 4, 0, 0, kindUintRaw, kindChain, 0, 2},
		[]byte{kindChain, 0, 0, kindBool, 1},
		[]byte{kindChain, 2, 2, kindIntTable, 3},
		bytes.Repeat([]byte{kindMap, 5, 1}, 30),
	)
}
