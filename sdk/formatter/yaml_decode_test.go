package formatter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/formatter"
)

// TestMakeOutput_YAMLScalars pins how the Lua and table formats read the
// scalars of a console answer: the YAML 1.1 booleans are booleans, a
// timestamp stays the text it is, numbers are read in every base, explicit
// tags win, and a quoted scalar is a string.
func TestMakeOutput_YAMLScalars(t *testing.T) {
	const bools = "---\n- on\n- off\n- y\n- n\n- Yes\n- NO\n- 'yes'\n- true\n- yEs\n...\n"

	cases := []struct {
		format formatter.Format
		input  string
		output string
	}{
		{formatter.LuaFormat, bools,
			"true, false, true, false, true, false, \"yes\", true, \"yEs\";\n"},
		{formatter.TableFormat, bools,
			"+-------+\n| col1  |\n+-------+\n| true  |\n+-------+\n| false |\n+-------+\n" +
				"| true  |\n+-------+\n| false |\n+-------+\n| true  |\n+-------+\n| false |\n" +
				"+-------+\n| yes   |\n+-------+\n| true  |\n+-------+\n| yEs   |\n+-------+\n"},
		{formatter.TTableFormat, bools,
			"+------+------+-------+------+-------+------+-------+-----+------+-----+\n" +
				"| col1 | true | false | true | false | true | false | yes | true | yEs |\n" +
				"+------+------+-------+------+-------+------+-------+-----+------+-----+\n"},
		{formatter.LuaFormat,
			"---\n- 2024-01-02\n- 2001-12-14t21:59:43.10-05:00\n- 2001-12-14 21:59:43.10 -5\n" +
				"- '2024-01-02'\n...\n",
			"\"2024-01-02\", \"2001-12-14t21:59:43.10-05:00\", \"2001-12-14 21:59:43.10 -5\", " +
				"\"2024-01-02\";\n"},
		{formatter.TableFormat,
			"---\n- 2024-01-02\n- 2001-12-14t21:59:43.10-05:00\n- 2001-12-14 21:59:43.10 -5\n" +
				"- '2024-01-02'\n...\n",
			"+------------------------------+\n| col1                         |\n" +
				"+------------------------------+\n| 2024-01-02                   |\n" +
				"+------------------------------+\n| 2001-12-14t21:59:43.10-05:00 |\n" +
				"+------------------------------+\n| 2001-12-14 21:59:43.10 -5    |\n" +
				"+------------------------------+\n| 2024-01-02                   |\n" +
				"+------------------------------+\n"},
		{formatter.TTableFormat,
			"---\n- 2024-01-02\n- 2001-12-14t21:59:43.10-05:00\n- 2001-12-14 21:59:43.10 -5\n" +
				"- '2024-01-02'\n...\n",
			"+------+------------+------------------------------+---------------------------+" +
				"------------+\n" +
				"| col1 | 2024-01-02 | 2001-12-14t21:59:43.10-05:00 | 2001-12-14 21:59:43.10 -5 |" +
				" 2024-01-02 |\n" +
				"+------+------------+------------------------------+---------------------------+" +
				"------------+\n"},
		{formatter.LuaFormat,
			"--- [0x1F, 0o17, 0777, 1e3, 1.5, -0, 18446744073709551615, -9223372036854775808, " +
				".inf, -.inf, 1_000, 190:20:30, 08]\n...\n",
			"31, 15, 511, 1000, 1.5, 0, 18446744073709551615, -9223372036854775808, +Inf, -Inf, " +
				"1000, \"190:20:30\", 8;\n"},
		{formatter.TTableFormat,
			"--- [0x1F, 0o17, 0777, 1e3, 1.5, -0, 18446744073709551615, -9223372036854775808, " +
				".inf, -.inf, 1_000, 190:20:30, 08]\n...\n",
			"+------+----+----+-----+------+-----+---+----------------------+" +
				"----------------------+------+------+------+-----------+---+\n" +
				"| col1 | 31 | 15 | 511 | 1000 | 1.5 | 0 | 18446744073709551615 |" +
				" -9223372036854775808 | +Inf | -Inf | 1000 | 190:20:30 | 8 |\n" +
				"+------+----+----+-----+------+-----+---+----------------------+" +
				"----------------------+------+------+------+-----------+---+\n"},
		{formatter.LuaFormat,
			"---\n- !!binary aGVsbG8=\n- !!str 123\n- !!bool yes\n- !!timestamp 2001-01-01\n" +
				"- ''\n- 'multi\n\n  line'\n...\n",
			"\"hello\", \"123\", true, \"2001-01-01\", \"\", \"multi\nline\";\n"},
		{formatter.TableFormat,
			"---\n- !!binary aGVsbG8=\n- !!str 123\n- !!bool yes\n- !!timestamp 2001-01-01\n" +
				"- ''\n- 'multi\n\n  line'\n...\n",
			"+------------+\n| col1       |\n+------------+\n| hello      |\n+------------+\n" +
				"| 123        |\n+------------+\n| true       |\n+------------+\n" +
				"| 2001-01-01 |\n+------------+\n|            |\n+------------+\n" +
				"| multi      |\n| line       |\n+------------+\n"},
		{formatter.LuaFormat, "---\n- [on, off, 2024-01-02, 1.5, null]\n...\n",
			"{true, false, \"2024-01-02\", 1.5, nil};\n"},
		{formatter.TableFormat, "---\n- [on, off, 2024-01-02, 1.5, null]\n...\n",
			"+------+-------+------------+------+------+\n" +
				"| col1 | col2  | col3       | col4 | col5 |\n" +
				"+------+-------+------------+------+------+\n" +
				"| true | false | 2024-01-02 | 1.5  | nil  |\n" +
				"+------+-------+------------+------+------+\n"},
		{formatter.TTableFormat, "---\n- [on, off, 2024-01-02, 1.5, null]\n...\n",
			"+------+------------+\n| col1 | true       |\n+------+------------+\n" +
				"| col2 | false      |\n+------+------------+\n| col3 | 2024-01-02 |\n" +
				"+------+------------+\n| col4 | 1.5        |\n+------+------------+\n" +
				"| col5 | nil        |\n+------+------------+\n"},
	}

	for _, tc := range cases {
		t.Run(tc.format.String(), func(t *testing.T) {
			output, err := formatter.MakeOutput(tc.format, tc.input, testOpts())
			require.NoError(t, err)
			assert.Equal(t, tc.output, output)
		})
	}
}

// TestMakeOutput_YAMLMaps pins how the Lua and table formats read the maps
// of a console answer: keys of any type, maps inside maps, which a table
// cell shows as JSON, anchors, merge keys, a repeated key and the result
// of an SQL statement.
func TestMakeOutput_YAMLMaps(t *testing.T) {
	const (
		keys = "---\n- {1: 'a'}\n- {'a': 1}\n- {1: {2: 'x'}}\n- {'a': {1: 'x'}}\n- {on: 1}\n" +
			"- {~: 1}\n- {2024-01-02: 1}\n...\n"
		anchors = "---\n- &a {x: 1}\n- *a\n- {<<: *a, x: 2}\n- {x: 2, <<: *a}\n" +
			"- {k: 1, k: 2}\n...\n"
		rows = "---\n- {'name': on, 'date': 2024-01-02, 'n': 1}\n" +
			"- {'name': off, 'date': '2024-01-03', 'n': 2}\n...\n"
		cells = "---\n- {'id': 1, 'meta': {1: 'x', 'k': on, 'when': 2024-01-02}}\n" +
			"- {'id': 2, 'meta': [{3: 4}]}\n...\n"
		sql = "---\n- metadata:\n  - name: COLUMN_1\n    type: string\n  - name: D\n" +
			"    type: string\n  rows:\n  - [on, 2024-01-02]\n  - ['off', y]\n...\n"
		mixed = "---\n- {1: 'a', 2: 'b', 'x': on}\n...\n"
	)

	cases := []struct {
		format formatter.Format
		input  string
		output string
	}{
		{formatter.LuaFormat, keys,
			"{[1] = \"a\"}, {a = 1}, {[1] = {[2] = \"x\"}}, {a = {[1] = \"x\"}}, {[true] = 1}, " +
				"{[<nil>] = 1}, {2024-01-02 = 1};\n"},
		{formatter.TableFormat, keys,
			"+------+\n| col1 |\n+------+\n| a    |\n+------+\n+---+\n| a |\n+---+\n| 1 |\n" +
				"+---+\n+-----------+\n| col1      |\n+-----------+\n| {\"2\":\"x\"} |\n" +
				"+-----------+\n+-----------+\n| a         |\n+-----------+\n| {\"1\":\"x\"} |\n" +
				"+-----------+\n+------+\n| true |\n+------+\n| 1    |\n+------+\n+-------+\n" +
				"| <nil> |\n+-------+\n| 1     |\n+-------+\n+------------+\n| 2024-01-02 |\n" +
				"+------------+\n| 1          |\n+------------+\n"},
		{formatter.TTableFormat, keys,
			"+------+---+\n| col1 | a |\n+------+---+\n+---+---+\n| a | 1 |\n+---+---+\n" +
				"+------+-----------+\n| col1 | {\"2\":\"x\"} |\n+------+-----------+\n" +
				"+---+-----------+\n| a | {\"1\":\"x\"} |\n+---+-----------+\n+------+---+\n" +
				"| true | 1 |\n+------+---+\n+-------+---+\n| <nil> | 1 |\n+-------+---+\n" +
				"+------------+---+\n| 2024-01-02 | 1 |\n+------------+---+\n"},
		{formatter.LuaFormat, anchors, "{x = 1}, {x = 1}, {x = 2}, {x = 1}, {k = 2};\n"},
		{formatter.TableFormat, anchors,
			"+---+\n| x |\n+---+\n| 1 |\n+---+\n| 1 |\n+---+\n| 2 |\n+---+\n| 1 |\n+---+\n" +
				"+---+\n| k |\n+---+\n| 2 |\n+---+\n"},
		{formatter.TTableFormat, anchors,
			"+---+---+---+---+---+\n| x | 1 | 1 | 2 | 1 |\n+---+---+---+---+---+\n" +
				"+---+---+\n| k | 2 |\n+---+---+\n"},
		{formatter.LuaFormat, "---\n- [[1, [2, [3]]], []]\n- {}\n- []\n...\n",
			"{{1, {2, {3}}}, {}}, {}, {};\n"},
		{formatter.TableFormat, "---\n- [[1, [2, [3]]], []]\n- {}\n- []\n...\n", "\n"},
		{formatter.TableFormat, rows,
			"+------------+---+-------+\n| date       | n | name  |\n" +
				"+------------+---+-------+\n| 2024-01-02 | 1 | true  |\n" +
				"+------------+---+-------+\n| 2024-01-03 | 2 | false |\n" +
				"+------------+---+-------+\n"},
		{formatter.TTableFormat, rows,
			"+------+------------+------------+\n| date | 2024-01-02 | 2024-01-03 |\n" +
				"+------+------------+------------+\n| n    | 1          | 2          |\n" +
				"+------+------------+------------+\n| name | true       | false      |\n" +
				"+------+------------+------------+\n"},
		{formatter.TableFormat, cells,
			"+----+----------------------------------------+\n" +
				"| id | meta                                   |\n" +
				"+----+----------------------------------------+\n" +
				"| 1  | {\"1\":\"x\",\"k\":true,\"when\":\"2024-01-02\"} |\n" +
				"+----+----------------------------------------+\n" +
				"| 2  | [{\"3\":4}]                              |\n" +
				"+----+----------------------------------------+\n"},
		{formatter.TTableFormat, cells,
			"+------+----------------------------------------+-----------+\n" +
				"| id   | 1                                      | 2         |\n" +
				"+------+----------------------------------------+-----------+\n" +
				"| meta | {\"1\":\"x\",\"k\":true,\"when\":\"2024-01-02\"} | [{\"3\":4}] |\n" +
				"+------+----------------------------------------+-----------+\n"},
		{formatter.TableFormat, sql,
			"+----------+------------+\n| COLUMN_1 | D          |\n+----------+------------+\n" +
				"| true     | 2024-01-02 |\n+----------+------------+\n" +
				"| off      | true       |\n+----------+------------+\n"},
		{formatter.TTableFormat, sql,
			"+----------+------------+------+\n| COLUMN_1 | true       | off  |\n" +
				"+----------+------------+------+\n| D        | 2024-01-02 | true |\n" +
				"+----------+------------+------+\n"},
		{formatter.TableFormat, mixed,
			"+------+------+------+\n| col1 | col2 | x    |\n+------+------+------+\n" +
				"| a    | b    | true |\n+------+------+------+\n"},
		{formatter.TTableFormat, mixed,
			"+------+------+\n| col1 | a    |\n+------+------+\n| col2 | b    |\n" +
				"+------+------+\n| x    | true |\n+------+------+\n"},
	}

	for _, tc := range cases {
		t.Run(tc.format.String(), func(t *testing.T) {
			output, err := formatter.MakeOutput(tc.format, tc.input, testOpts())
			require.NoError(t, err)
			assert.Equal(t, tc.output, output)
		})
	}
}

// TestMakeOutput_YAMLNotASequence pins the errors for an answer that is not a
// sequence of values, which no console sends.
func TestMakeOutput_YAMLNotASequence(t *testing.T) {
	cases := []struct {
		format formatter.Format
		input  string
		errMsg string
	}{
		{formatter.LuaFormat, "--- 42\n...\n",
			"cannot render lua: yaml: unmarshal errors:\n" +
				"  line 1: cannot unmarshal !!int `42` into []interface {}"},
		{formatter.LuaFormat, "--- {a: 1, a: 2}\n...\n",
			"cannot render lua: yaml: unmarshal errors:\n" +
				"  line 1: cannot unmarshal !!map into []interface {}"},
		{formatter.TableFormat, "--- 42\n...\n",
			"not yaml array, cannot render tables: yaml: unmarshal errors:\n" +
				"  line 1: cannot unmarshal !!int `42` into []formatter.lazyMessage"},
		{formatter.TTableFormat, "--- {a: 1}\n...\n",
			"not yaml array, cannot render tables: yaml: unmarshal errors:\n" +
				"  line 1: cannot unmarshal !!map into []formatter.lazyMessage"},
		{formatter.LuaFormat, "--- [1, 2\n...\n",
			"cannot render lua: yaml: line 1: did not find expected ',' or ']'"},
	}

	for _, tc := range cases {
		t.Run(tc.format.String(), func(t *testing.T) {
			output, err := formatter.MakeOutput(tc.format, tc.input, testOpts())
			require.EqualError(t, err, tc.errMsg)
			assert.Empty(t, output)
		})
	}
}

func testOpts() formatter.Opts {
	return formatter.Opts{
		Graphics:       true,
		ColumnWidthMax: 0,
		TableDialect:   formatter.DefaultTableDialect,
	}
}
