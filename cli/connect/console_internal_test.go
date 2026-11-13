package connect

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestPrintPush pins how the console prints a pushed value: block YAML with
// an indent of two, a sequence in a map at the indent of its key, the
// strings YAML would read as something else quoted.
func TestPrintPush(t *testing.T) {
	cases := []struct {
		name   string
		pushed any
		want   string
	}{
		{"string", "xx", "xx\n\n"},
		{"long word", strings.Repeat("x", 100), strings.Repeat("x", 100) + "\n\n"},
		{
			"long line in a map", map[any]any{"key": strings.Repeat("word ", 20) + "end"},
			"key: " + strings.Repeat("word ", 15) + "word\n  " + strings.Repeat("word ", 4) +
				"end\n\n",
		},
		{"number", 31, "31\n\n"},
		{"float", 1.5, "1.5\n\n"},
		{"bool", true, "true\n\n"},
		{"null", nil, "null\n\n"},
		{"empty", "", "\"\"\n\n"},
		{
			"quoted", []any{"yes", "on", "y", "123", "2024-01-02", "~", "null", "a: b", "- x"},
			"- \"yes\"\n- \"on\"\n- \"y\"\n- \"123\"\n- \"2024-01-02\"\n- \"~\"\n- \"null\"\n" +
				"- 'a: b'\n- '- x'\n\n",
		},
		{"multiline", "line1\nline2", "|-\n  line1\n  line2\n\n"},
		{
			"map",
			map[any]any{
				1: "a", "b": []any{2, map[any]any{"c": []any{true, false}}}, "e": map[any]any{},
			},
			"1: a\nb:\n- 2\n- c:\n  - true\n  - false\ne: {}\n\n",
		},
		{"nested arrays", []any{[]any{1, []any{2}}, []any{}}, "- - 1\n  - - 2\n- []\n\n"},
		{
			"short line", "a string that is long, but not longer than the eighty columns of a line",
			"a string that is long, but not longer than the eighty columns of a line\n\n",
		},
		{
			"long line",
			"a string that is longer than the eighty columns of a line, which the printer folds " +
				"at a space past the column",
			"a string that is longer than the eighty columns of a line, which the printer folds\n" +
				"  at a space past the column\n\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			printPush(&out, tc.pushed)
			assert.Equal(t, tc.want, out.String())
		})
	}
}
