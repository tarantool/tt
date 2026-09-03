package formatter

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"
)

// luaEncodeElement encodes element to a Lua-compatible string.
func luaEncodeElement(elem any) string {
	switch t := elem.(type) {
	case map[any]any:
		res := "{"
		first := true

		var resSb15 strings.Builder

		for k, v := range t {
			if !first {
				resSb15.WriteString(", ")
			}

			if str, ok := k.(string); ok {
				fmt.Fprintf(&resSb15, "%s = %s", str, luaEncodeElement(v))
			} else {
				fmt.Fprintf(&resSb15, "[%v] = %s", k, luaEncodeElement(v))
			}

			first = false
		}

		res += resSb15.String()

		return res + "}"
	case []any:
		res := "{"

		var resSb29 strings.Builder

		for k, v := range t {
			resSb29.WriteString(luaEncodeElement(v))

			if k < len(t)-1 {
				resSb29.WriteString(", ")
			}
		}

		res += resSb29.String()

		return res + "}"
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
	if err == nil {
		var res string

		var resSb57 strings.Builder

		for i, unpackedVal := range decoded {
			if i < len(decoded)-1 {
				resSb57.WriteString(luaEncodeElement(unpackedVal) + ", ")
			} else {
				resSb57.WriteString(luaEncodeElement(unpackedVal))
			}
		}

		res += resSb57.String()

		return res + ";\n", nil
	} else {
		return "", fmt.Errorf("cannot render lua: %w", err)
	}
}
