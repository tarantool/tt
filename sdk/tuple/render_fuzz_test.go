package tuple_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/tuple"
)

// The float32 Lua builds: the literal, as a double, cast to a float.
const (
	luaFloat32Prefix = "require('ffi').cast('float', "
	luaFloat64Prefix = "require('ffi').cast('double', "
	luaRawPrefix     = "require('msgpack').object_from_raw("
)

// floatFields are the fields a float is written in by FuzzFloatText.
func floatFields(is32 bool) []tuple.Field {
	sized := tuple.FieldFloat64
	if is32 {
		sized = tuple.FieldFloat32
	}

	fields := make([]tuple.Field, 0, 10)

	for _, typ := range []tuple.FieldType{
		tuple.FieldAny, tuple.FieldScalar, tuple.FieldNumber, tuple.FieldDouble, sized,
	} {
		fields = append(fields, tuple.Field{Name: "f", Type: typ, IsNullable: false},
			tuple.Field{Name: "f", Type: typ, IsNullable: true})
	}

	return fields
}

// floatOptions are the options a float is written with by FuzzFloatText:
// the defaults, a point for thousands, a space for thousands and a null
// that looks like a number.
var floatOptions = []tuple.FieldOptions{
	{},
	{ThousandSeparators: ".", DecimalSeparators: ","},
	{ThousandSeparators: " `", DecimalSeparators: ".,"},
	{Null: "0"},
	{Null: "NaN"},
}

// FuzzFloatText checks the texts of a float: they read back bit for bit,
// with the exponent where encoding/json writes one, in every renderer and in
// every field that holds the float.
func FuzzFloatText(f *testing.F) {
	for _, number := range float64Table {
		f.Add(math.Float64bits(number), false)
	}

	for _, number := range float32Table {
		f.Add(uint64(math.Float32bits(number)), true)
	}

	f.Fuzz(func(t *testing.T, bits uint64, is32 bool) {
		var (
			value   any
			number  float64
			bitSize = 64
		)

		if is32 {
			small := math.Float32frombits(uint32(bits)) //nolint:gosec // The low bits.

			value, number, bitSize = small, float64(small), 32
		} else {
			number = math.Float64frombits(bits)
			value = number
		}

		finite := !math.IsNaN(number) && !math.IsInf(number, 0)

		text, ok := tuple.Text(value)
		require.True(t, ok)

		jsonOut, err := tuple.AppendJSON(nil, value)
		require.NoError(t, err)

		if finite {
			want, err := json.Marshal(value)
			require.NoError(t, err)
			require.Equal(t, string(want), text, "the text is not that of encoding/json")
			//nolint:testifylint // The bytes count, not the JSON value.
			require.Equal(t, string(want), string(jsonOut))
			requireFloatText(t, text, number, bitSize)
		} else {
			want := map[bool]string{true: "Infinity", false: "-Infinity"}[number > 0]
			if math.IsNaN(number) {
				want = "NaN"
			}

			require.Equal(t, want, text)
			//nolint:testifylint // The bytes count, not the JSON value.
			require.Equal(t, strconv.Quote(want), string(jsonOut))
		}

		checkFloatYAML(t, value, number, bitSize)
		checkFloatLua(t, value, number, bitSize)
		checkTaggedJSON(t, value)

		for _, opts := range floatOptions {
			for _, field := range floatFields(is32) {
				checkFieldRoundTrip(t, value, field, opts)
			}
		}
	})
}

// requireFloatText checks that text is the shortest decimal that reads back
// as number.
func requireFloatText(t *testing.T, text string, number float64, bitSize int) {
	t.Helper()

	back, err := strconv.ParseFloat(text, bitSize)
	require.NoError(t, err, text)
	require.Equal(t, math.Float64bits(number), math.Float64bits(back), text)

	shortest := strconv.FormatFloat(number, 'e', -1, bitSize)
	mantissa, _, _ := strings.Cut(shortest, "e")
	digits := strings.TrimLeft(strings.ReplaceAll(strings.TrimPrefix(mantissa, "-"), ".", ""),
		"0")

	textMantissa, _, _ := strings.Cut(strings.TrimPrefix(text, "-"), "e")
	got := strings.Trim(strings.ReplaceAll(textMantissa, ".", ""), "0")
	require.Equal(t, strings.TrimRight(digits, "0"), got, "%s is not the shortest", text)
}

func checkFloatYAML(t *testing.T, value any, number float64, bitSize int) {
	t.Helper()

	out, err := yaml.Marshal(tuple.Value{V: value})
	require.NoError(t, err)

	var doc yaml.Node

	require.NoError(t, yaml.Unmarshal(out, &doc), "%q", out)
	expectYAMLFloat(t, doc.Content[0], number, bitSize, string(out))

	if !math.IsNaN(number) && !math.IsInf(number, 0) {
		require.True(t, strings.ContainsAny(doc.Content[0].Value, ".e"),
			"%q reads as an integer", out)
	}
}

// checkFloatLua checks the Lua of a float: a literal that reads back as it -
// a float32 after it is read as a double and cast, as LuaJIT does, an
// integral float64 cast to a double - the expression of an infinity, or the
// MessagePack of a NaN.
func checkFloatLua(t *testing.T, value any, number float64, bitSize int) {
	t.Helper()

	literal := string(tuple.AppendLua(nil, value))

	if math.IsNaN(number) {
		raw, err := tuple.AppendMsgpack(nil, value)
		require.NoError(t, err)
		require.Equal(t, string(tuple.AppendLuaString([]byte(luaRawPrefix), string(raw)))+")",
			literal)

		return
	}

	var prefix string

	switch {
	case bitSize == 32:
		prefix = luaFloat32Prefix
	case number == math.Trunc(number) && !math.IsInf(number, 0):
		prefix = luaFloat64Prefix
	}

	if prefix != "" {
		require.True(t, strings.HasPrefix(literal, prefix) && strings.HasSuffix(literal, ")"),
			literal)

		literal = literal[len(prefix) : len(literal)-1]
	}

	switch {
	case math.IsInf(number, 1):
		require.Equal(t, "math.huge", literal)

		return
	case math.IsInf(number, -1):
		require.Equal(t, "-math.huge", literal)

		return
	}

	require.True(t, strings.ContainsAny(literal, ".e"), "%s reads as an integer", literal)

	double, err := strconv.ParseFloat(literal, 64)
	require.NoError(t, err, literal)

	back := double
	if bitSize == 32 {
		back = float64(float32(double))
	}

	require.Equal(t, math.Float64bits(number), math.Float64bits(back),
		"the Lua %s reads back as %v", literal, back)
}

// checkFieldRoundTrip checks that a value a field holds is written in it
// with a text that reads back as the value and is written again the same.
func checkFieldRoundTrip(t *testing.T, value any, field tuple.Field, opts tuple.FieldOptions) {
	t.Helper()

	if knownBug(value) != "" {
		return
	}

	msg := fmt.Sprintf("%s in %+v with %+v", describe(value), field, opts)

	text, err := tuple.AppendField(nil, value, field, opts)
	require.NoError(t, err, msg)

	back, err := tuple.ParseField(string(text), field, opts)
	require.NoError(t, err, "%s: %q", msg, text)
	requireSame(t, value, back, "%s: %q", msg, text)

	again, err := tuple.AppendField(nil, back, field, opts)
	require.NoError(t, err, msg)
	require.Equal(t, string(text), string(again), msg)
}

// FuzzAppendLuaString checks that the Lua string of any text reads back as
// it, with no raw control byte and, for a text that is not UTF-8, ASCII
// only.
func FuzzAppendLuaString(f *testing.F) {
	for _, seed := range trickyStrings {
		f.Add(seed, "x = ")
	}

	f.Fuzz(func(t *testing.T, text, prefix string) {
		out := tuple.AppendLuaString([]byte(prefix), text)
		require.Equal(t, prefix, string(out[:len(prefix)]))

		literal := out[len(prefix):]

		back, err := unquoteLua(string(literal))
		require.NoError(t, err, "%q", literal)
		require.Equal(t, text, back, "%q", literal)

		for _, char := range literal {
			require.False(t, char < ' ' || char == 0x7f, "a raw control byte in %q", literal)
		}

		if utf8.ValidString(text) {
			require.True(t, utf8.Valid(literal), "%q", literal)
		} else {
			for _, char := range literal {
				require.Less(t, char, byte(utf8.RuneSelf), "a byte beyond ASCII in %q", literal)
			}
		}

		require.NoError(t, luaBalanced(literal))
	})
}

// FuzzRenderers checks every renderer on a generated value, on a value
// Decode reads and on a value of the types go-tarantool decodes into, which
// must render as its normalized form.
func FuzzRenderers(f *testing.F) {
	limitMemory(f)

	for _, seed := range generatorSeeds() {
		for mode := range byte(3) {
			f.Add(append([]byte{mode}, seed...))
		}
	}

	for _, value := range seedValues(f) {
		f.Add(append([]byte{1}, appendMsgpack(f, nil, value)...))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}

		input := data[1:]

		switch data[0] % 3 {
		case 0:
			checkRenderers(t, genValue(t, input))
		case 1:
			value, err := tuple.Decode(input)
			if err == nil {
				checkRenderers(t, value)
			}
		default:
			foreign := genForeign(t, &fuzzSource{data: bytes.Clone(input), pos: 0}, 0)
			normal := normalizeForeign(foreign)
			checkForeign(t, foreign, normal)
			checkRenderers(t, decodeModel(t, normal))
		}
	})
}
