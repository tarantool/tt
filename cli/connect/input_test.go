package connect_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/tarantool/tt/cli/connect"
)

func TestNewLuaValidator(t *testing.T) {
	s := NewLuaValidator()
	assert.NoError(t, s.Close())
}

func TestNewLuaValidator_implementsValidateCloser(t *testing.T) {
	var s ValidateCloser = NewLuaValidator()
	defer s.Close()
}

func TestLuaValidator_Close_notCreated(t *testing.T) {
	s := LuaValidator{}
	assert.NoError(t, s.Close())
}

func TestLuaValidator_Close_multipleTimes(t *testing.T) {
	s := NewLuaValidator()
	assert.NoError(t, s.Close())
	assert.NoError(t, s.Close())
}

func TestLuaValidator_Validate_aferClose(t *testing.T) {
	s := NewLuaValidator()
	s.Close()
	assert.Panics(t, func() { s.Validate("any string") })
}

func TestLuaValidator_Validate_true(t *testing.T) {
	s := NewLuaValidator()
	defer s.Close()

	cases := []string{
		"\"string\"",
		"'string'",
		"\"string\" .. 'string'",
		"41",
		"41 + 13",
		"local x",
		"local x = 10",
		"x = 10",
		"x == 10",
		"return x",
		"return",
		"return x; return x;",
		"for i = 1,10 do end",
		"for i = 1,10 do call(x) end",
		"if i == 1 then else end",
		"if i == 1 then call(x) else xcall(y) end",
		"if i = 1 then else",
		"if i = 1 then else end",
		"require(\"asd\")",
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			ret := s.Validate(c)
			assert.True(t, ret)
		})
	}
}

func TestLuaValidator_Validate_false(t *testing.T) {
	s := NewLuaValidator()
	defer s.Close()

	cases := []string{
		"\"string",
		"'string",
		"\"string .. string'",
		"for i = 1,10 do",
		"for i = 1,10 do call(x)",
		"if i == 1 then else",
		"if i == 1 then call(x) else xcall(y)",
		"require(\"asd\"",
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			ret := s.Validate(c)
			assert.False(t, ret)
		})
	}
}

func TestLuaValidatorValidate_mixed(t *testing.T) {
	s := NewLuaValidator()
	defer s.Close()

	ret := s.Validate("do")
	assert.False(t, ret)

	ret = s.Validate("\"any invalid string\"")
	assert.True(t, ret)
}

func TestNewSQLValidator_implementsValidateCloser(t *testing.T) {
	var _ ValidateCloser = NewSQLValidator()
}

func TestSQLValidator_Close_multiple(t *testing.T) {
	v := NewSQLValidator()
	assert.NoError(t, v.Close())
	assert.NoError(t, v.Close())
}

func TestSQLValidator_Validate(t *testing.T) {
	v := NewSQLValidator()
	assert.True(t, v.Validate("any"))
	assert.True(t, v.Validate("any other"))
}

func TestSQLValidator_Validate_afterClose(t *testing.T) {
	v := NewSQLValidator()
	assert.True(t, v.Validate("any"))
	assert.NoError(t, v.Close())
	assert.True(t, v.Validate("any"))
}

type ValidatorMock struct {
	in  string
	ret bool
}

func (m *ValidatorMock) Validate(str string) bool {
	m.in = str
	return m.ret
}

func TestAddStmtPart(t *testing.T) {
	validator := &ValidatorMock{}
	const stmt = "1part"
	const part = "2part"
	const expected = "1part\n2part"

	for _, c := range []bool{false, true} {
		name := "false"
		if c == true {
			name = "true"
		}
		t.Run(name, func(t *testing.T) {
			validator.ret = c
			result, completed := AddStmtPart(stmt, part, "", validator)
			assert.Equal(t, expected, result)
			assert.Equal(t, c, completed)
			assert.Equal(t, expected, validator.in)
		})
	}
}

func TestAddStmtPart_luaValidator(t *testing.T) {
	validator := NewLuaValidator()
	defer validator.Close()

	parts := []struct {
		str       string
		expected  string
		completed bool
	}{
		{"   ", "", true},
		{"for i = 1,10 do", "for i = 1,10 do", false},
		{"    print(x)", "for i = 1,10 do\n    print(x)", false},
		{"    local j = 5", "for i = 1,10 do\n    print(x)\n    local j = 5", false},
		{"", "for i = 1,10 do\n    print(x)\n    local j = 5\n", false},
		{" ", "for i = 1,10 do\n    print(x)\n    local j = 5\n\n ", false},
		{"end", "for i = 1,10 do\n    print(x)\n    local j = 5\n\n \nend", true},
	}

	stmt := ""
	for _, part := range parts {
		var completed bool
		stmt, completed = AddStmtPart(stmt, part.str, "", validator)

		assert.Equal(t, part.expected, stmt)
		assert.Equal(t, part.completed, completed)
	}
}

func TestAddStmtPart_luaValidator_Delimiter(t *testing.T) {
	validator := NewLuaValidator()
	defer validator.Close()

	parts := []struct {
		str       string
		expected  string
		delim     string
		completed bool
	}{
		{
			"   ",
			"",
			"",
			true,
		},
		{
			"for i = 1,10 do ; ",
			"for i = 1,10 do ",
			";",
			false,
		},
		{
			"    print(x)",
			"for i = 1,10 do \n    print(x)",
			"",
			false,
		},
		{
			"    local j = 5</br>  ",
			"for i = 1,10 do \n    print(x)\n    local j = 5",
			"</br>",
			false,
		},
		{
			"",
			"for i = 1,10 do \n    print(x)\n    local j = 5\n",
			";",
			false,
		},
		{
			" ",
			"for i = 1,10 do \n    print(x)\n    local j = 5\n\n ",
			"",
			false,
		},
		{
			"end\t***  ",
			"for i = 1,10 do \n    print(x)\n    local j = 5\n\n \nend\t",
			"***",
			true,
		},
	}

	stmt := ""
	for _, part := range parts {
		var completed bool
		stmt, completed = AddStmtPart(stmt, part.str, part.delim, validator)

		assert.Equal(t, part.expected, stmt)
		assert.Equal(t, part.completed, completed)
	}
}
