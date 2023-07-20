package formatter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/formatter"
)

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
