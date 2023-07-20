package connect

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/tarantool/tt/cli/connector"
)

const (
	defaultStr = ""
	luaStr     = "lua"
	sqlStr     = "sql"
)

// Language defines a set of supported languages.
type Language int

const (
	DefaultLanguage Language = iota
	LuaLanguage
	SQLLanguage
)

// ParseLanguage parses a language string representation. It supports mixed
// case letters.
func ParseLanguage(str string) (Language, bool) {
	str = strings.ToLower(str)
	switch str {
	case defaultStr:
		return DefaultLanguage, true
	case luaStr:
		return LuaLanguage, true
	case sqlStr:
		return SQLLanguage, true
	}
	return DefaultLanguage, false
}

// String returns a string representation of the language.
func (l Language) String() string {
	switch l {
	case DefaultLanguage:
		return defaultStr
	case LuaLanguage:
		return luaStr
	case SQLLanguage:
		return sqlStr
	default:
		panic("Unknown language")
	}
}

// ChangeLanguage changes a language for a connection.
func ChangeLanguage(evaler connector.Evaler, lang Language) error {
	if lang == DefaultLanguage {
		lang = LuaLanguage
	}

	languageCmd := setLanguagePrefix + " " + lang.String()
	response, err := evaler.Eval(consoleEvalFuncBody,
		[]interface{}{languageCmd},
		connector.RequestOpts{},
	)
	if err != nil {
		return err
	}

	if len(response) == 0 {
		return fmt.Errorf("unexpected response: empty")
	} else if len(response) > 1 {
		return fmt.Errorf("unexpected response: %v", response)
	}

	var ret string
	var ok bool
	if ret, ok = response[0].(string); !ok {
		return fmt.Errorf("unexpected response: %v", response)
	}

	var decoded interface{}
	if err = yaml.Unmarshal([]byte(ret), &decoded); err != nil {
		return fmt.Errorf("unable to decode response: %w", err)
	}

	var decodedArray []interface{}
	if decodedArray, ok = decoded.([]interface{}); !ok || len(decodedArray) != 1 {
		return fmt.Errorf("unexpected response: %s", ret)
	}

	var value bool
	if value, ok = decodedArray[0].(bool); !ok {
		return fmt.Errorf("unexpected response: %s", ret)
	}

	if !value {
		return fmt.Errorf("%s returns false", languageCmd)
	}

	return nil
}
