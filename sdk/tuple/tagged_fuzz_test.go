package tuple_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"

	"github.com/tarantool/tt/sdk/tuple"
)

// taggedTags are the tags of tagged JSON: an object of one key that is one
// of them is a tagged value, any other object a map.
var taggedTags = []string{
	tuple.TagBinary, tuple.TagRawString, tuple.TagDouble, tuple.TagFloat32, tuple.TagDecimal,
	tuple.TagUUID, tuple.TagDatetime, tuple.TagInterval, tuple.TagExt, tuple.TagMap,
}

// jsonNode is a JSON value as its text has it: the order and the repeats of
// the keys of an object kept.
type jsonNode struct {
	delim  json.Delim
	scalar any
	keys   []string
	items  []jsonNode
}

// readJSONNode reads a value from dec, which uses numbers.
func readJSONNode(dec *json.Decoder) (jsonNode, error) {
	token, err := dec.Token()
	if err != nil {
		return jsonNode{}, err
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return jsonNode{delim: 0, scalar: token, keys: nil, items: nil}, nil
	}

	node := jsonNode{delim: delim, scalar: nil, keys: nil, items: nil}

	for dec.More() {
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return jsonNode{}, err
			}

			text, _ := key.(string)

			node.keys = append(node.keys, text)
		}

		item, err := readJSONNode(dec)
		if err != nil {
			return jsonNode{}, err
		}

		node.items = append(node.items, item)
	}

	_, err = dec.Token()

	return node, err
}

// jsonNumberValue is the value ParseTaggedJSON documents for a number: an
// integer a uint64, or an int64 when it is negative, any other number a
// float64.
func jsonNumberValue(text string) (any, bool) {
	if !strings.ContainsAny(text, ".eE") {
		integer, ok := new(big.Int).SetString(text, 10)
		if ok && integer.Sign() >= 0 && integer.IsUint64() {
			return integer.Uint64(), true
		}

		if ok && integer.Sign() < 0 && integer.IsInt64() {
			return integer.Int64(), true
		}
	}

	number, err := strconv.ParseFloat(text, 64)

	return number, err == nil
}

// expectTagged checks that value is what ParseTaggedJSON documents for a
// node.
//
//nolint:cyclop // One case per kind of node is the clearest form.
func expectTagged(t *testing.T, node jsonNode, value any) {
	t.Helper()

	switch node.delim {
	case 0:
		if number, ok := node.scalar.(json.Number); ok {
			want, ok := jsonNumberValue(string(number))
			require.True(t, ok, number)
			requireSame(t, want, value, "the number %s", number)

			return
		}

		require.Equal(t, node.scalar, value)
	case '[':
		items, ok := value.([]any)
		require.True(t, ok, "an array is %s", describe(value))
		require.Len(t, items, len(node.items))

		for index, item := range node.items {
			expectTagged(t, item, items[index])
		}
	case '{':
		if len(node.keys) == 1 && slices.Contains(taggedTags, node.keys[0]) {
			expectTag(t, node.keys[0], node.items[0], value)

			return
		}

		items, ok := value.(tuple.Map)
		require.True(t, ok, "an object of keys %q is %s", node.keys, describe(value))
		require.Len(t, items, len(node.keys))

		for index, key := range node.keys {
			require.Equal(t, key, items[index].Key)
			expectTagged(t, node.items[index], items[index].Value)
		}
	}
}

// expectTag checks that value is the value of a tag and its payload.
//
//nolint:cyclop // One case per tag is the clearest form.
func expectTag(t *testing.T, tag string, payload jsonNode, value any) {
	t.Helper()

	text, _ := payload.scalar.(string)

	switch tag {
	case tuple.TagBinary:
		data, err := base64.StdEncoding.DecodeString(text)
		require.NoError(t, err)
		requireSame(t, tuple.Binary(data), value)
	case tuple.TagRawString:
		data, err := base64.StdEncoding.DecodeString(text)
		require.NoError(t, err)
		require.Equal(t, string(data), value)
	case tuple.TagDouble:
		require.Contains(t, []string{"NaN", "Infinity", "-Infinity"}, text)

		number, err := strconv.ParseFloat(text, 64)
		require.NoError(t, err)
		requireSame(t, number, value)
	case tuple.TagFloat32:
		number, _ := payload.scalar.(json.Number)
		if text == "" {
			text = string(number)
		}

		parsed, err := strconv.ParseFloat(text, 32)
		require.NoError(t, err)
		requireSame(t, float32(parsed), value)
	case tuple.TagDecimal:
		_, ok := value.(decimal.Decimal)
		require.True(t, ok, describe(value))
	case tuple.TagUUID:
		requireSame(t, uuid.MustParse(text), value)
	case tuple.TagDatetime:
		moment, err := tuple.ParseDatetime(text)
		require.NoError(t, err)
		requireSame(t, moment, value)
	case tuple.TagInterval:
		ival, err := tuple.ParseInterval(text)
		require.NoError(t, err)
		requireSame(t, ival, value)
	case tuple.TagExt:
		expectExtTag(t, payload, value)
	case tuple.TagMap:
		items, ok := value.(tuple.Map)
		require.True(t, ok, describe(value))
		require.Equal(t, json.Delim('['), payload.delim)
		require.Len(t, items, len(payload.items))

		for index, pair := range payload.items {
			require.Len(t, pair.items, 2)
			expectTagged(t, pair.items[0], items[index].Key)
			expectTagged(t, pair.items[1], items[index].Value)
		}
	}
}

// expectExtTag checks that value is what Decode reads from the extension of
// a TagExt.
func expectExtTag(t *testing.T, payload jsonNode, value any) {
	t.Helper()

	var (
		typ  int64
		data []byte
	)

	for index, key := range payload.keys {
		item := payload.items[index].scalar

		switch key {
		case "type":
			number, _ := item.(json.Number)

			parsed, err := strconv.ParseInt(string(number), 10, 8)
			require.NoError(t, err)

			typ = parsed
		case "data":
			text, _ := item.(string)

			decoded, err := base64.StdEncoding.DecodeString(text)
			require.NoError(t, err)

			data = decoded
		}
	}

	want, err := tuple.Decode(tuple.AppendExtMsgpack(nil, int8(typ), data))
	require.NoError(t, err)
	requireSame(t, want, value)
}

// hasUnknownAdjust reports whether value has an interval of an adjust mode
// Tarantool has no name for, which tagged JSON keeps and MessagePack, as
// go-tarantool encodes it, does not.
func hasUnknownAdjust(value any) bool {
	switch typed := value.(type) {
	case datetime.Interval:
		return typed.Adjust < datetime.NoneAdjust || typed.Adjust > datetime.LastAdjust
	case []any:
		return slices.ContainsFunc(typed, hasUnknownAdjust)
	case tuple.Map:
		return slices.ContainsFunc(typed, func(item tuple.MapItem) bool {
			return hasUnknownAdjust(item.Key) || hasUnknownAdjust(item.Value)
		})
	}

	return false
}

// taggedSeeds are texts of tagged JSON for the seed corpus.
var taggedSeeds = []string{
	` [ 1 , -1, 1.0, 1e2, 18446744073709551616, -0, -0.0 ] `,
	`{"b": 1, "a": {"$uuid": "` + sampleUUID + `"}}`, `{"$map": [[1, 2], [1, 3]]}`,
	`{"$datetime": "2024-11-14"}`, `{"$float32": 0.1}`, `{"$float32": "NaN"}`,
	`{"$double": "Infinity"}`, `{"$ext": {"data": "Bw==", "type": 66}}`,
	`{"$ext": {"type": 2, "data": "tUFH/VOLTTmzrRe6cnYvSA=="}}`, `{"$rawstring": "/w=="}`,
	`"é"`, `{"$nope": 1}`, `{"$uuid": "x", "a": 1}`, `{"a": 1, "$uuid": "x"}`,
	`{"$map": []}`, `{"$map": [], "$map": []}`, `{"$binary": ""}`, `{"$decimal": "1E+999999"}`,
	`{"$decimal": "-0.000"}`, `{"$interval": "+1 years, adjust 7"}`, `{"": null}`, `{"$": 1}`,
	`{"$datetime": "2024-10-27T02:30:00+0100 Europe/Berlin"}`, `{"$map": [[{"$map": []}, []]]}`,
	`{"$ext": {"type": 1, "data": "AhUM"}}`, `"\ud800"`, `"\u0000"`, `1e400`, `[1,`,
	strings.Repeat("[", 512) + strings.Repeat("]", 512), strings.Repeat(`{"$map":[[1,`, 300),
	strings.Repeat(`{"a":`, 513) + "1" + strings.Repeat("}", 513),
}

// FuzzParseTaggedJSON checks that ParseTaggedJSON reports only its error,
// reads what its documentation says, no deeper than Decode, a value whose
// tagged JSON reads back as it and is written the same again, and whose
// MessagePack Decode reads back.
func FuzzParseTaggedJSON(f *testing.F) {
	limitMemory(f)

	for _, seed := range taggedSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, text []byte) {
		value, err := tuple.ParseTaggedJSON(text)
		if err != nil {
			require.ErrorIs(t, err, tuple.ErrTaggedJSON)

			return
		}

		require.True(t, json.Valid(text), "%q is not JSON", text)
		require.LessOrEqual(t, valueDepth(value), decodeDepth)

		dec := json.NewDecoder(bytes.NewReader(text))
		dec.UseNumber()

		node, err := readJSONNode(dec)
		require.NoError(t, err)
		expectTagged(t, node, value)

		written, err := tuple.AppendTaggedJSON(nil, value)
		require.NoError(t, err)

		back, err := tuple.ParseTaggedJSON(written)
		require.NoError(t, err, "%q", written)
		requireSame(t, value, back, "%q", written)

		again, err := tuple.AppendTaggedJSON(nil, back)
		require.NoError(t, err)
		require.Equal(t, string(written), string(again))

		if hasUnknownAdjust(value) {
			return
		}

		requireSame(t, value, decodeModel(t, value))

		raw, err := tuple.AppendMsgpack(nil, value)
		require.NoError(t, err)
		require.Equal(t, appendMsgpack(t, nil, value), raw)
	})
}

// FuzzTaggedJSONValue checks that a value of any kind reads back from its
// tagged JSON.
func FuzzTaggedJSONValue(f *testing.F) {
	limitMemory(f)

	for _, seed := range generatorSeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		value := genValue(t, data)
		skipKnownBugs(t, value)

		text, err := tuple.AppendTaggedJSON(nil, value)
		require.NoError(t, err)

		back, err := tuple.ParseTaggedJSON(text)
		require.NoError(t, err, "%s: %q", describe(value), text)
		requireSame(t, value, back, "%q", text)

		again, err := tuple.AppendTaggedJSON(nil, back)
		require.NoError(t, err)
		require.Equal(t, string(text), string(again))
	})
}

// FuzzAppendMsgpack checks AppendMsgpack against the canonical encoder of the
// tests: on a generated value, on a value Decode reads, on a value of the
// types go-tarantool decodes into, and on a generated value before Decode
// normalizes it, which may hold a decimal Tarantool does not.
func FuzzAppendMsgpack(f *testing.F) {
	limitMemory(f)

	for _, seed := range generatorSeeds() {
		for mode := range byte(4) {
			f.Add(append([]byte{mode}, seed...))
		}
	}

	for _, seed := range decodeSeeds(f) {
		f.Add(append([]byte{1}, seed...))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}

		input := data[1:]

		switch data[0] % 4 {
		case 0:
			checkAppendMsgpack(t, genValue(t, input))
		case 1:
			value, err := tuple.Decode(input)
			if err == nil {
				checkAppendMsgpack(t, value)
			}
		case 2:
			foreign := genForeign(t, &fuzzSource{data: bytes.Clone(input), pos: 0}, 0)

			raw, err := tuple.AppendMsgpack(nil, foreign)
			require.NoError(t, err)
			require.Equal(t, appendMsgpack(t, nil, normalizeForeign(foreign)), raw)
		default:
			gen := generator{tb: t, src: &fuzzSource{data: input, pos: 0}, budget: 64}
			value := gen.value(0)

			raw, err := tuple.AppendMsgpack(nil, value)
			if err != nil {
				require.True(t, hasInvalidDecimal(t, value), "%s: %v", describe(value), err)

				return
			}

			require.Equal(t, appendMsgpack(t, nil, value), raw, describe(value))
		}
	})
}

// checkAppendMsgpack checks that AppendMsgpack keeps dst, writes what the
// canonical encoder does, and that Decode reads the value back.
func checkAppendMsgpack(t *testing.T, value any) {
	t.Helper()

	prefix := []byte{0x93}

	raw, err := tuple.AppendMsgpack(bytes.Clone(prefix), value)
	require.NoError(t, err, describe(value))
	require.Equal(t, prefix, raw[:len(prefix)])

	raw = raw[len(prefix):]
	require.Equal(t, appendMsgpack(t, nil, value), raw, describe(value))

	back, err := tuple.Decode(raw)
	require.NoError(t, err)
	requireSame(t, value, back)
}

// hasInvalidDecimal reports whether value has a decimal Tarantool does not
// hold, which Decode reads as an Ext.
func hasInvalidDecimal(t *testing.T, value any) bool {
	t.Helper()

	switch typed := value.(type) {
	case decimal.Decimal:
		_, isExt := decodeModel(t, typed).(tuple.Ext)

		return isExt
	case []any:
		return slices.ContainsFunc(typed, func(item any) bool { return hasInvalidDecimal(t, item) })
	case tuple.Map:
		return slices.ContainsFunc(typed, func(item tuple.MapItem) bool {
			return hasInvalidDecimal(t, item.Key) || hasInvalidDecimal(t, item.Value)
		})
	}

	return false
}
