package connect_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/tarantool/tt/cli/connect"
)

func TestLanguage_ParseLanguage(t *testing.T) {
	cases := []struct {
		str      string
		expected Language
		ok       bool
	}{
		{"", DefaultLanguage, true},
		{"lua", LuaLanguage, true},
		{"LuA", LuaLanguage, true},
		{"LUA", LuaLanguage, true},
		{"sql", SQLLanguage, true},
		{"SqL", SQLLanguage, true},
		{"SQL", SQLLanguage, true},
		{".", DefaultLanguage, false},
		{"12", DefaultLanguage, false},
		{"luaasd", DefaultLanguage, false},
		{"lua123", DefaultLanguage, false},
	}

	for _, c := range cases {
		t.Run(c.str, func(t *testing.T) {
			lang, ok := ParseLanguage(c.str)
			assert.Equal(t, c.ok, ok, "Unexpected result")
			if ok {
				assert.Equal(t, c.expected, lang, "Unexpected language")
			}
		})
	}
}

func TestLanguage_String(t *testing.T) {
	cases := []struct {
		language Language
		expected string
		panic    bool
	}{
		{DefaultLanguage, "", false},
		{LuaLanguage, "lua", false},
		{SQLLanguage, "sql", false},
		{Language(666), "Unknown language", true},
	}

	for _, c := range cases {
		t.Run(c.expected, func(t *testing.T) {
			if c.panic {
				f := func() { _ = c.language.String() }
				assert.PanicsWithValue(t, "Unknown language", f)
			} else {
				result := c.language.String()
				assert.Equal(t, c.expected, result, "Unexpected result")
			}
		})
	}
}
