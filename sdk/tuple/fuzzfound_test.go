package tuple_test

import (
	"encoding/hex"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/decimal"
	"gopkg.in/yaml.v3"

	"github.com/tarantool/tt/sdk/tuple"
)

// The bugs the fuzz targets found, each pinned by its input.

// TestAmbiguousZone checks a datetime of an abbreviation Tarantool takes for
// ambiguous, such as PST, which a datetime it stores may still have: its Text
// is the one Tarantool prints and, like in Tarantool, does not read back, but
// its tagged form, with its offset, does.
func TestAmbiguousZone(t *testing.T) {
	t.Parallel()

	// 1970-01-01T00:00:00Z: its seconds, its nanoseconds, its offset -480 and
	// the zone of index 269, PST.
	raw, err := hex.DecodeString("d804" + "0000000000000000" + "00000000" + "20fe" + "0d01")
	require.NoError(t, err)

	value := mustDecode(t, raw)

	text, _ := tuple.Text(value)
	assert.Equal(t, "1969-12-31T16:00:00 PST", text)

	_, err = tuple.ParseDatetime(text)
	require.ErrorIs(t, err, tuple.ErrDatetime)

	for _, typ := range []tuple.FieldType{tuple.FieldDatetime, tuple.FieldAny} {
		field := tuple.Field{Type: typ}

		written, err := tuple.AppendField(nil, value, field, tuple.FieldOptions{})
		require.NoError(t, err)

		back, err := tuple.ParseField(string(written), field, tuple.FieldOptions{})
		require.NoError(t, err)

		again, err := tuple.AppendMsgpack(nil, back)
		require.NoError(t, err)
		assert.Equal(t, raw, again, string(written))
	}

	assert.Equal(t, "(require('datetime').parse('1969-12-31T16:00:00-0800'))",
		string(tuple.AppendLua(nil, value)))
}

// TestYAMLNodeLineBreaks checks that a string with a line break or another
// control character reads back from its YAML, which yaml.v3 would put in a
// block that does not.
func TestYAMLNodeLineBreaks(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"\n", "\na", "\t\n", "a\r\nb", "\x00"} {
		out, err := yaml.Marshal(tuple.Value{V: text})
		require.NoError(t, err)

		var back string

		require.NoError(t, yaml.Unmarshal(out, &back), "%q", out)
		assert.Equal(t, text, back, "%q", out)
	}
}

// TestParseTaggedJSONLinear checks that objects whose first key is a tag,
// nested and around a long text, are read in one pass: each is read once
// whether it is a tagged value or a map.
func TestParseTaggedJSONLinear(t *testing.T) {
	big := `"` + strings.Repeat("a", 1<<20) + `"`

	for _, text := range []string{
		strings.Repeat(`{"$map":[[`, 500) + big + strings.Repeat(`,1]]}`, 500),
		strings.Repeat(`{"$binary":`, 500) + big + strings.Repeat(`,"x":1}`, 500),
	} {
		var before, after runtime.MemStats

		runtime.ReadMemStats(&before)

		_, err := tuple.ParseTaggedJSON([]byte(text))
		require.NoError(t, err)

		runtime.ReadMemStats(&after)

		const limit = 64 << 20

		assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(limit))
	}
}

// TestAppendLuaIntegerKeys checks that a map of keys Lua takes for those of
// an array stays a map.
func TestAppendLuaIntegerKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []any{uint64(1), uint64(2), math.Inf(1)} {
		assert.Contains(t, string(tuple.AppendLua(nil, tuple.Map{{Key: key, Value: "a"}})),
			"{__serialize = 'map'}", "%v", key)
	}

	// An integral float64 is a C double, not a Lua number.
	for _, key := range []any{"1", int64(-1), 0.5, 1e21, uint64(1) << 60} {
		assert.NotContains(t, string(tuple.AppendLua(nil, tuple.Map{{Key: key, Value: "a"}})),
			"__serialize", "%v", key)
	}
}

// TestAppendFieldPointThousands checks a number with a point for a thousand
// separator and no decimal separator: it is written with an exponent.
func TestAppendFieldPointThousands(t *testing.T) {
	t.Parallel()

	opts := tuple.FieldOptions{ThousandSeparators: "."}

	for _, test := range []struct {
		value any
		typ   tuple.FieldType
		want  string
	}{
		{1.5, tuple.FieldDouble, "15e-1"},
		{3.0, tuple.FieldAny, "30e-1"},
		{0.000001, tuple.FieldNumber, "1e-6"},
		{math.Copysign(0, -1), tuple.FieldFloat64, "-0e-1"},
		{1.5e21, tuple.FieldDouble, "15e20"},
		{decimal.MustNewDecimal("12.340"), tuple.FieldDecimal, "12340e-3"},
	} {
		field := tuple.Field{Type: test.typ}

		text, err := tuple.AppendField(nil, test.value, field, opts)
		require.NoError(t, err, "%v", test.value)
		assert.Equal(t, test.want, string(text))

		back, err := tuple.ParseField(string(text), field, opts)
		require.NoError(t, err)
		assertSameValue(t, test.value, back)
	}
}

// TestSkippedDay checks a day a zone skipped: Samoa went from 2011-12-29 to
// 2011-12-31, and a time of the 30th is one of the 31st, as Tarantool reads
// it.
func TestSkippedDay(t *testing.T) {
	t.Parallel()

	_, err := time.LoadLocation("Pacific/Apia")
	if err != nil {
		t.Skip("no time zone database")
	}

	value, err := tuple.ParseDatetime("2011-12-30T12:00:00 Pacific/Apia")
	require.NoError(t, err)

	moment := value.ToTime()
	assert.Equal(t, 31, moment.Day())
	assert.Equal(t, "Pacific/Apia", moment.Location().String())

	_, err = tuple.ParseDatetime("2011-02-29T12:00:00 Pacific/Apia")
	require.ErrorIs(t, err, tuple.ErrDatetime)
}
