package connect

import (
	"fmt"
	"strings"
	"unicode"

	lua "github.com/yuin/gopher-lua"
)

// Validator is the interface that wraps basic validate methods for a
// string statement.
type Validator interface {
	// Validate returns true if the string is a completed statement.
	Validate(str string) bool
}

// ValidateCloser is the interface that wraps basic validate methods
// for a string statement and Close() methods.
type ValidateCloser interface {
	Validator
	// Close closes the validator.
	Close() error
}

// LuaValidator implements ValidateCloser interface for the Lua language.
type LuaValidator struct {
	state *lua.LState
}

// NewLuaValidator returns a LuaValidator object.
func NewLuaValidator() *LuaValidator {
	return &LuaValidator{
		state: lua.NewState(),
	}
}

// Validate returns true if the string is a completed statement for the Lua
// language.
func (s *LuaValidator) Validate(str string) bool {
	if s.state == nil {
		panic("the validator is closed or created incorrectly")
	}

	// See:
	// https://github.com/tarantool/tarantool/blob/b53cb2aeceedc39f356ceca30bd0087ee8de7c16/src/box/lua/console.lua#L575
	if _, err := s.state.LoadString(str); err == nil ||
		!strings.Contains(err.Error(), "at EOF") {
		// Valid Lua code or a syntax error not due to an incomplete input.
		return true
	}

	if _, err := s.state.LoadString(fmt.Sprintf("return %s", str)); err == nil {
		// Certain obscure inputs like '(42\n)' yield the same error as
		// incomplete statement.
		return true
	}

	return false
}

// Close closes the Lua validator. It is safe to call it multiple times.
func (s *LuaValidator) Close() error {
	if s.state != nil {
		s.state.Close()
		s.state = nil
	}
	return nil
}

// SQLValidator implements ValidateCloser interface for the SQL language.
type SQLValidator struct{}

// NewSQLValidator return a SQLValidator object.
func NewSQLValidator() *SQLValidator {
	return &SQLValidator{}
}

// Validate always returns true.
func (v SQLValidator) Validate(_ string) bool {
	return true
}

// Close closes the SQL validator. It is safe to call it multiple times.
func (v SQLValidator) Close() error {
	return nil
}

// cleanupDelimiter checks if the statement ends with the string `delim`. If yes, it removes it.
// Returns true if the delimiter has been removed.
func cleanupDelimiter(stmt, delim string) (string, bool) {
	if delim == "" {
		return stmt, true
	}
	no_space := strings.TrimRightFunc(stmt, func(r rune) bool {
		return unicode.IsSpace(r)
	})
	no_delim := strings.TrimSuffix(no_space, delim)
	if len(no_space) > len(no_delim) {
		return no_delim, true
	}
	return stmt, false
}

// AddStmtPart adds a new part of the statement. It returns a result statement
// and true if the statement is already completed.
func AddStmtPart(stmt, part, delim string, validator Validator) (string, bool) {
	if stmt == "" {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			stmt = part
		}
	} else {
		stmt += "\n" + part
	}

	var hasDelim bool
	stmt, hasDelim = cleanupDelimiter(stmt, delim)
	return stmt, hasDelim && validator.Validate(stmt)
}
