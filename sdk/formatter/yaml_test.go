package formatter_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/v3/formatter"
)

func TestDecodeYAML_Any(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  any
	}{
		{"empty", "", nil},
		{"null document", "---\n...\n", nil},
		{"old bools", "[y, Y, yes, Yes, YES, on, On, ON, n, N, no, No, NO, off, Off, OFF]",
			[]any{true, true, true, true, true, true, true, true,
				false, false, false, false, false, false, false, false}},
		{"not bools", "[yEs, tRUE, 'on', !!str off, \"no\"]",
			[]any{"yEs", "tRUE", "on", "off", "no"}},
		{"tagged bool", "[!!bool yes, !!bool off]", []any{true, false}},
		{"timestamps", "[2024-01-02, 2001-12-14T21:59:43Z, !!timestamp 2001-01-01]",
			[]any{"2024-01-02", "2001-12-14T21:59:43Z", "2001-01-01"}},
		{"numbers", "[0x1F, 0o17, 0777, 1_000, 1.5, .inf, 18446744073709551615]",
			[]any{31, 15, 511, 1000, 1.5, math.Inf(1), uint64(18446744073709551615)}},
		{"maps", "{1: a, b: {2: c}, on: d, ~: e}",
			map[any]any{1: "a", "b": map[any]any{2: "c"}, true: "d", nil: "e"}},
		{"repeated key", "{a: 1, a: 2}", map[any]any{"a": 2}},
		{"merge", "[&a {x: 1, z: 1}, {<<: *a, x: 2}, {x: 2, <<: *a}, {<<: [{x: 3}, *a]}]",
			[]any{
				map[any]any{"x": 1, "z": 1}, map[any]any{"x": 2, "z": 1},
				map[any]any{"x": 1, "z": 1}, map[any]any{"x": 3, "z": 1},
			}},
		{"quoted merge key", "{'<<': {x: 1}}", map[any]any{"<<": map[any]any{"x": 1}}},
		{"binary", "!!binary aGVsbG8=", "hello"},
		{"empty collections", "[[], {}]", []any{[]any{}, map[any]any{}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got any

			require.NoError(t, formatter.DecodeYAML([]byte(tc.input), &got))
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeYAML_Errors(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		errMsg string
	}{
		{"syntax", ",,,", "yaml: did not find expected node content"},
		{"anchor in itself", "&a [*a]", "yaml: anchor 'a' value contains itself"},
		{"sequence key", "{[1]: x}", "yaml: invalid map key: []interface {}{1}"},
		{"merge of a scalar", "{<<: 5}", "yaml: map merge requires map or sequence of maps"},
		{"merge of a scalar list", "{<<: [5]}", "yaml: map merge requires map or sequence of maps"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got any

			require.ErrorContains(t, formatter.DecodeYAML([]byte(tc.input), &got), tc.errMsg)
		})
	}
}

func TestDecodeYAML_Sequence(t *testing.T) {
	var got []any

	require.NoError(t, formatter.DecodeYAML([]byte("--- [1, on]\n...\n"), &got))
	assert.Equal(t, []any{1, true}, got)

	got = []any{1}
	require.NoError(t, formatter.DecodeYAML([]byte("---\n...\n"), &got))
	assert.Nil(t, got)

	require.EqualError(t, formatter.DecodeYAML([]byte("{a: 1, a: 2}"), &got),
		"yaml: unmarshal errors:\n  line 1: cannot unmarshal !!map into []interface {}")
	require.EqualError(t, formatter.DecodeYAML([]byte("!custom {a: 1}"), &got),
		"yaml: unmarshal errors:\n  line 1: cannot unmarshal !!map into []interface {}")
	require.EqualError(t, formatter.DecodeYAML([]byte("42"), &got),
		"yaml: unmarshal errors:\n  line 1: cannot unmarshal !!int `42` into []interface {}")
}

func TestDecodeYAML_Map(t *testing.T) {
	var got map[string]any

	input := "1: a\nyes: b\n0x10: c\n~: d\nk: {1: on}\n"
	require.NoError(t, formatter.DecodeYAML([]byte(input), &got))
	assert.Equal(t, map[string]any{
		"1": "a", "yes": "b", "0x10": "c", "": "d", "k": map[any]any{1: true},
	}, got)

	require.NoError(t, formatter.DecodeYAML([]byte(""), &got))
	assert.Nil(t, got)

	require.EqualError(t, formatter.DecodeYAML([]byte("[a]"), &got),
		"yaml: unmarshal errors:\n  line 1: cannot unmarshal !!seq into map[string]interface {}")

	// A key that is no scalar is skipped, the others are kept.
	err := formatter.DecodeYAML([]byte("[1]: a\nb: c\n? {x: 1}\n: d\n"), &got)
	require.EqualError(t, err, "yaml: unmarshal errors:\n"+
		"  line 1: cannot unmarshal !!seq into string\n"+
		"  line 3: cannot unmarshal !!map into string")
	assert.Equal(t, map[string]any{"b": "c"}, got)
}

func TestDecodeYAML_Target(t *testing.T) {
	var got []string

	require.ErrorContains(t, formatter.DecodeYAML([]byte("[a]"), &got),
		"DecodeYAML decodes into *any, *[]any or *map[string]any, not *[]string")
}

func TestEncodeYAML(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"scalar", "xx", "xx\n"},
		{"null", nil, "null\n"},
		{"quoted", []any{"yes", "y", "123", "2024-01-02", "<<", ""},
			"- \"yes\"\n- \"y\"\n- \"123\"\n- \"2024-01-02\"\n- <<\n- \"\"\n"},
		{"map", map[any]any{"b": []any{1, map[any]any{"c": []any{2}}}, 1: "a"},
			"1: a\nb:\n- 1\n- c:\n  - 2\n"},
		{"nested sequences", []any{[]any{1, []any{2}}}, "- - 1\n  - - 2\n"},
		{
			"long line", strings.Repeat("word ", 20) + "end",
			strings.Repeat("word ", 16) + "word\n  " + strings.Repeat("word ", 3) + "end\n",
		},
		{
			"long line in a map", map[any]any{"key": strings.Repeat("word ", 20) + "end"},
			"key: " + strings.Repeat("word ", 15) + "word\n  " + strings.Repeat("word ", 4) +
				"end\n",
		},
		{"long word", strings.Repeat("x", 100), strings.Repeat("x", 100) + "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatter.EncodeYAML(tc.value)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(got))
		})
	}
}
