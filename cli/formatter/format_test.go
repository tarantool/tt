package formatter_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/formatter"
)

func TestFormatter_ParseFormat(t *testing.T) {
	cases := []struct {
		str      string
		expected formatter.Format
		ok       bool
	}{
		{"yaml", formatter.DefaultFormat, true},
		{"yaml", formatter.YamlFormat, true},
		{"lua", formatter.LuaFormat, true},
		{"lua", formatter.LuaFormat, true},
		{"table", formatter.TableFormat, true},
		{"ttable", formatter.TTableFormat, true},
	}

	for _, c := range cases {
		t.Run(c.str, func(t *testing.T) {
			format, ok := formatter.ParseFormat(c.str)
			assert.Equal(t, c.ok, ok, "Unexpected result")
			if ok {
				assert.Equal(t, c.expected, format, "Unexpected output format")
			}
		})
	}
}

func TestFormatter_Format_String(t *testing.T) {
	cases := []struct {
		format   formatter.Format
		expected string
		panic    bool
	}{
		{formatter.DefaultFormat, "yaml", false},
		{formatter.YamlFormat, "yaml", false},
		{formatter.LuaFormat, "lua", false},
		{formatter.TableFormat, "table", false},
		{formatter.TTableFormat, "ttable", false},
		{formatter.Format(2023), "Unknown output format", true},
	}

	for _, c := range cases {
		t.Run(c.expected, func(t *testing.T) {
			if c.panic {
				f := func() { _ = c.format.String() }
				assert.PanicsWithValue(t, "Unknown output format", f)
			} else {
				result := c.format.String()
				assert.Equal(t, c.expected, result, "Unexpected result")
			}
		})
	}
}

func TestFormatter_MakeOutputFormat(t *testing.T) {
	cases := []struct {
		outputFormat formatter.Format
		input        string // string from server side
		output       string // render content to console
		panic        bool
	}{
		{
			// when user typed to console: localhost:xxxx> 1,2,3
			formatter.DefaultFormat,
			"---\n- 1\n- 2\n- 3\n...",
			"---\n- 1\n- 2\n- 3\n...\n",
			false,
		},
		{
			// when user typed to console: localhost:xxxx> 1,2,3
			formatter.YamlFormat,
			"---\n- 1\n- 2\n- 3\n...",
			"---\n- 1\n- 2\n- 3\n...\n",
			false,
		},
		{
			// when user typed to console: localhost:xxxx>
			formatter.LuaFormat,
			"---\n...\n",
			";\n",
			false,
		},
		{
			// when user typed to console: localhost:xxxx> 1,2,3
			formatter.LuaFormat,
			"--- [1, 2, 3]\n...",
			"1, 2, 3;\n",
			false,
		},
		{
			// when user typed to console: localhost:xxxx> 1,2,3
			formatter.TableFormat,
			"--- [1, 2, 3]\n...",
			"+------+\n" +
				"| col1 |\n" +
				"+------+\n" +
				"| 1    |\n" +
				"+------+\n" +
				"| 2    |\n" +
				"+------+\n" +
				"| 3    |\n" +
				"+------+\n",
			false,
		},
		{
			// when user typed to console: localhost:xxxx> 1,2,3
			formatter.TTableFormat,
			"--- [1, 2, 3]\n...",
			"+------+---+---+---+\n" +
				"| col1 | 1 | 2 | 3 |\n" +
				"+------+---+---+---+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> box.tuple.new({1,2,'hello'}), box.tuple.new({10,20})
			formatter.DefaultFormat,
			"---\n- [1, 2, 'hello']\n- [10, 20]\n...",
			"---\n- [1, 2, 'hello']\n- [10, 20]\n...\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> box.tuple.new({1,2,'hello'}), box.tuple.new({10,20})
			formatter.YamlFormat,
			"---\n- [1, 2, 'hello']\n- [10, 20]\n...",
			"---\n- [1, 2, 'hello']\n- [10, 20]\n...\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> box.tuple.new({1,2,'hello'}), box.tuple.new({10,20})
			formatter.TableFormat,
			"---\n- [1, 2, 'hello']\n- [10, 20]\n...",
			"+------+------+-------+\n" +
				"| col1 | col2 | col3  |\n" +
				"+------+------+-------+\n" +
				"| 1    | 2    | hello |\n" +
				"+------+------+-------+\n" +
				"| 10   | 20   |       |\n" +
				"+------+------+-------+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> box.tuple.new({1,2,'hello'}), box.tuple.new({10,20})
			formatter.TTableFormat,
			"---\n- [1, 2, 'hello']\n- [10, 20]\n...",
			"+------+-------+----+\n" +
				"| col1 | 1     | 10 |\n" +
				"+------+-------+----+\n" +
				"| col2 | 2     | 20 |\n" +
				"+------+-------+----+\n" +
				"| col3 | hello |    |\n" +
				"+------+-------+----+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> true, false, {10,20,30}, box.NULL, {40,box.NULL,100}
			formatter.DefaultFormat,
			"---\n- true\n- false\n" +
				"- - 10\n  - 20\n  - 30\n- null\n- - 40\n  - null\n  - 100\n...",
			"---\n- true\n- false\n" +
				"- - 10\n  - 20\n  - 30\n- null\n- - 40\n  - null\n  - 100\n...\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> true, false, {10,20,30}, box.NULL, {40,box.NULL,100}
			formatter.YamlFormat,
			"---\n- true\n- false\n" +
				"- - 10\n  - 20\n  - 30\n- null\n- - 40\n  - null\n  - 100\n...",
			"---\n- true\n- false\n" +
				"- - 10\n  - 20\n  - 30\n- null\n- - 40\n  - null\n  - 100\n...\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> true, false, {10,20,30}, box.NULL, {40,box.NULL,100}
			formatter.LuaFormat,
			"---\n- true\n- false\n" +
				"- - 10\n  - 20\n  - 30\n- null\n- - 40\n  - null\n  - 100\n...",
			"true, false, {10, 20, 30}, nil, {40, nil, 100};\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> true, false, {10,20,30}, box.NULL, {40,box.NULL,100}
			formatter.TableFormat,
			"---\n- true\n- false\n" +
				"- - 10\n  - 20\n  - 30\n- null\n- - 40\n  - null\n  - 100\n...",
			"+-------+\n" +
				"| col1  |\n" +
				"+-------+\n" +
				"| true  |\n" +
				"+-------+\n" +
				"| false |\n" +
				"+-------+\n" +
				"+------+------+------+\n" +
				"| col1 | col2 | col3 |\n" +
				"+------+------+------+\n" +
				"| 10   | 20   | 30   |\n" +
				"+------+------+------+\n" +
				"+------+\n" +
				"| col1 |\n" +
				"+------+\n" +
				"| nil  |\n" +
				"+------+\n" +
				"+------+------+------+\n" +
				"| col1 | col2 | col3 |\n" +
				"+------+------+------+\n" +
				"| 40   | nil  | 100  |\n" +
				"+------+------+------+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> true, false, {10,20,30}, box.NULL, {40,box.NULL,100}
			formatter.TTableFormat,
			"---\n- true\n- false\n" +
				"- - 10\n  - 20\n  - 30\n- null\n- - 40\n  - null\n  - 100\n...",
			"+------+------+-------+\n" +
				"| col1 | true | false |\n" +
				"+------+------+-------+\n" +
				"+------+----+\n" +
				"| col1 | 10 |\n" +
				"+------+----+\n" +
				"| col2 | 20 |\n" +
				"+------+----+\n" +
				"| col3 | 30 |\n" +
				"+------+----+\n" +
				"+------+-----+\n" +
				"| col1 | nil |\n" +
				"+------+-----+\n" +
				"+------+-----+\n" +
				"| col1 | 40  |\n" +
				"+------+-----+\n" +
				"| col2 | nil |\n" +
				"+------+-----+\n" +
				"| col3 | 100 |\n" +
				"+------+-----+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> {data=123,'hello'},{data=321,'world'}
			formatter.TableFormat,
			"---\n [{'data': 123, 1: 'hello'}, {'data': 321, 1: 'world'}]\n...\n",
			"+-------+------+\n" +
				"| col1  | data |\n" +
				"+-------+------+\n" +
				"| hello | 123  |\n" +
				"+-------+------+\n" +
				"| world | 321  |\n" +
				"+-------+------+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> {data=123,'hello'},{data=321,'world'}
			formatter.TTableFormat,
			"---\n [{'data': 123, 1: 'hello'}, {'data': 321, 1: 'world'}]\n...\n",
			"+------+-------+-------+\n" +
				"| col1 | hello | world |\n" +
				"+------+-------+-------+\n" +
				"| data | 123   | 321   |\n" +
				"+------+-------+-------+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> box.execute('select 1 as foo, 30, 50, 4+4 as data')
			formatter.TableFormat,
			"---\n- DATA: 8\n  COLUMN_1: 30\n  FOO: 1\n  COLUMN_2: 50\n...\n",
			"+----------+----------+------+-----+\n" +
				"| COLUMN_1 | COLUMN_2 | DATA | FOO |\n" +
				"+----------+----------+------+-----+\n" +
				"| 30       | 50       | 8    | 1   |\n" +
				"+----------+----------+------+-----+\n",
			false,
		},
		{
			// when user typed to console:
			// localhost:xxxx> box.execute('select 1 as foo, 30, 50, 4+4 as data')
			formatter.TTableFormat,
			"---\n- DATA: 8\n  COLUMN_1: 30\n  FOO: 1\n  COLUMN_2: 50\n...\n",
			"+----------+----+\n" +
				"| COLUMN_1 | 30 |\n" +
				"+----------+----+\n" +
				"| COLUMN_2 | 50 |\n" +
				"+----------+----+\n" +
				"| DATA     | 8  |\n" +
				"+----------+----+\n" +
				"| FOO      | 1  |\n" +
				"+----------+----+\n",
			false,
		},
		{
			// panic case
			2023,
			"",
			"",
			true,
		},
		{
			// when user typed to console:
			// localhost:xxxx> \set output table
			// localhost:xxxx> {}
			formatter.TableFormat,
			"{}",
			"\n",
			false,
		},
		{
			formatter.TableFormat,
			"{},",
			"\n",
			false,
		},
		{
			formatter.TableFormat,
			"{};",
			"\n",
			false,
		},
		{
			formatter.TableFormat,
			"{{}}",
			"\n",
			false,
		},
		{
			formatter.TableFormat,
			"{{}",
			"\n",
			false,
		},
		{
			formatter.TableFormat,
			"{}}",
			"\n",
			false,
		},
	}

	for _, c := range cases {
		t.Run(fmt.Sprint(c.outputFormat), func(t *testing.T) {
			formatterOpts := formatter.Opts{
				Graphics:       true,
				ColumnWidthMax: 0,
				TableDialect:   formatter.DefaultTableDialect,
			}
			if c.panic {
				assert.PanicsWithValue(t, "Unknown render case", func() {
					formatter.MakeOutput(c.outputFormat, c.input, formatterOpts)
				})
			} else {
				output, err := formatter.MakeOutput(c.outputFormat, c.input, formatterOpts)
				assert.Equal(t, c.output, output, "Unexpected render output")
				assert.NoError(t, err)
			}
		})
	}
}
