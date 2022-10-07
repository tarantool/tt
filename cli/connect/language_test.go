package connect_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
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

type inputEvaler struct {
	fun  string
	args []interface{}
	opts connector.RequestOpts
}

func (evaler *inputEvaler) Eval(fun string,
	args []interface{}, opts connector.RequestOpts) ([]interface{}, error) {

	evaler.fun = fun
	evaler.args = args
	evaler.opts = opts

	return nil, errors.New("any error")
}

func TestChangeLanguage_requestInputs(t *testing.T) {
	expectedFun := "return require('console').eval(...)\n"
	expectedOpts := connector.RequestOpts{}
	cases := []struct {
		lang Language
		arg  string
	}{
		{DefaultLanguage, "\\set language lua"},
		{LuaLanguage, "\\set language lua"},
		{SQLLanguage, "\\set language sql"},
	}

	for _, c := range cases {
		t.Run(c.lang.String(), func(t *testing.T) {
			evaler := &inputEvaler{}
			ChangeLanguage(evaler, c.lang)
			assert.Equal(t, expectedFun, evaler.fun)
			assert.Equal(t, c.arg, evaler.args[0].(string))
			assert.Equal(t, expectedOpts, evaler.opts)
		})
	}
}

type outputEvaler struct {
	ret []interface{}
	err error
}

func (evaler outputEvaler) Eval(f string, a []interface{},
	o connector.RequestOpts) ([]interface{}, error) {
	return evaler.ret, evaler.err
}

func TestChangeLanguage_requestOutputsValid(t *testing.T) {
	evaler := outputEvaler{ret: []interface{}{"- true"}}
	assert.NoError(t, ChangeLanguage(evaler, LuaLanguage))
}

func TestChangeLanguage_requestOutputsInvalid(t *testing.T) {
	cases := []struct {
		ret      []interface{}
		err      error
		expected string
	}{
		{nil, nil, "unexpected response: empty"},
		{nil, errors.New("any error"), "any error"},
		{[]interface{}{true}, nil, "unexpected response: [true]"},
		{[]interface{}{",,,"}, nil, "unable to decode response: yaml:" +
			" did not find expected node content"},
		{[]interface{}{"true"}, nil, "unexpected response: true"},
		{[]interface{}{"- true", "- true"}, nil, "unexpected response: [- true - true]"},
		{[]interface{}{"- true\n  true"}, nil, "unexpected response: - true\n  true"},
		{[]interface{}{"- 123"}, nil, "unexpected response: - 123"},
		{[]interface{}{"- false"}, nil, "\\set language lua returns false"},
	}

	for _, c := range cases {
		t.Run(c.expected, func(t *testing.T) {
			evaler := &outputEvaler{ret: c.ret, err: c.err}
			err := ChangeLanguage(evaler, LuaLanguage)

			assert.EqualError(t, err, c.expected)
		})
	}
}
