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
		var res strings.Builder
		res.WriteByte('{')
		first := true
		for k, v := range t {
			if !first {
				res.WriteString(", ")
			}
			if str, ok := k.(string); ok {
				fmt.Fprintf(&res, "%s = %s", str, luaEncodeElement(v))
			} else {
				fmt.Fprintf(&res, "[%v] = %s", k, luaEncodeElement(v))
			}
			first = false
		}
		res.WriteByte('}')
		return res.String()
	case []any:
		var res strings.Builder
		res.WriteByte('{')
		for k, v := range t {
			res.WriteString(luaEncodeElement(v))
			if k < len(t)-1 {
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
	if err := yaml.Unmarshal([]byte(input), &decoded); err == nil {
		var res strings.Builder
		for i, unpackedVal := range decoded {
			res.WriteString(luaEncodeElement(unpackedVal))
			if i < len(decoded)-1 {
				res.WriteString(", ")
			}
		}
		res.WriteString(";\n")
		return res.String(), nil
	} else {
		return "", fmt.Errorf("cannot render lua: %w", err)
	}
}
