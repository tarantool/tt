package formatter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/sdk/formatter"
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

	for _, tt := range cases {
		t.Run(tt.str, func(t *testing.T) {
			format, ok := formatter.ParseTableDialect(tt.str)
			assert.Equal(t, tt.ok, ok, "Unexpected result")

			if ok {
				assert.Equal(t, tt.expected, format, "Unexpected table dialect")
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

	for _, tt := range cases {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.panic {
				f := func() { _ = tt.tableDialect.String() }
				assert.PanicsWithValue(t, "Unknown table dialect", f)
			} else {
				result := tt.tableDialect.String()
				assert.Equal(t, tt.expected, result, "Unexpected result")
			}
		})
	}
}
