package formatter_test

import (
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
		{"", formatter.DefaultFormat, true},
		{"yaml", formatter.YamlFormat, true},
		{"y", formatter.YamlFormat, true},
		{"lua", formatter.LuaFormat, true},
		{"lua", formatter.LuaFormat, true},
		{"table", formatter.TableFormat, true},
		{"t", formatter.TableFormat, true},
		{"ttable", formatter.TTableFormat, true},
		{"T", formatter.TTableFormat, true},
		{".", formatter.DefaultFormat, false},
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
		{formatter.DefaultFormat, "", false},
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

func TestFormatter_ParseTableDialect(t *testing.T) {
	cases := []struct {
		str      string
		expected formatter.TableDialect
		ok       bool
	}{
		{"default", formatter.DefaultTableDialect, true},
		{"markdown", formatter.MarkdownTableDialect, true},
		{"jira", formatter.JiraTableDialect, true},
		{".", formatter.DefaultTableDialect, false},
	}

	for _, c := range cases {
		t.Run(c.str, func(t *testing.T) {
			format, ok := formatter.ParseTableDialect(c.str)
			assert.Equal(t, c.ok, ok, "Unexpected result")
			if ok {
				assert.Equal(t, c.expected, format, "Unexpected table dialect")
			}
		})
	}
}

func TestFormatter_TableDialect_String(t *testing.T) {
	cases := []struct {
		tableDialect formatter.TableDialect
		expected     string
		panic        bool
	}{
		{formatter.DefaultTableDialect, "default", false},
		{formatter.MarkdownTableDialect, "markdown", false},
		{formatter.JiraTableDialect, "jira", false},
		{formatter.TableDialect(2023), "Unknown table dialect", true},
	}

	for _, c := range cases {
		t.Run(c.expected, func(t *testing.T) {
			if c.panic {
				f := func() { _ = c.tableDialect.String() }
				assert.PanicsWithValue(t, "Unknown table dialect", f)
			} else {
				result := c.tableDialect.String()
				assert.Equal(t, c.expected, result, "Unexpected result")
			}
		})
	}
}
