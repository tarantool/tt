package formatter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"
)

// luaEncodeElement encodes element to a Lua-compatible string.
func luaEncodeElement(elem any) string {
	switch typed := elem.(type) {
	case map[any]any:
		var res strings.Builder

		res.WriteByte('{')

		first := true
		for key, value := range typed {
			if !first {
				res.WriteString(", ")
			}

			if str, ok := key.(string); ok {
				fmt.Fprintf(&res, "%s = %s", str, luaEncodeElement(value))
			} else {
				fmt.Fprintf(&res, "[%v] = %s", key, luaEncodeElement(value))
			}

			first = false
		}

		res.WriteByte('}')

		return res.String()
	case []any:
		var res strings.Builder

		res.WriteByte('{')

		for k, v := range typed {
			res.WriteString(luaEncodeElement(v))

			if k < len(typed)-1 {
				res.WriteString(", ")
			}
		}

		res.WriteByte('}')

		return res.String()
	default:
		if elem == nil {
			return "nil"
		}

		if str, ok := elem.(string); ok {
			return fmt.Sprintf(`"%v"`, str)
		}

		return fmt.Sprintf("%v", elem)
	}
}

// makeLuaOutput returns Lua-compatible string from the yaml string input.
func makeLuaOutput(input string) (string, error) {
	// Handle empty input from remote console.
	if input == "---\n...\n" {
		return ";\n", nil
	}

	var decoded []any

	err := yaml.Unmarshal([]byte(input), &decoded)
	if err != nil {
		return "", fmt.Errorf("cannot render lua: %w", err)
	}

	var res strings.Builder

	for i, unpackedVal := range decoded {
		res.WriteString(luaEncodeElement(unpackedVal))

		if i < len(decoded)-1 {
			res.WriteString(", ")
		}
	}

	res.WriteString(";\n")

	return res.String(), nil
}
