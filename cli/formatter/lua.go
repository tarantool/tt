package formatter

import (
	"fmt"

	"gopkg.in/yaml.v2"
)

// luaEncodeElement encodes element to a Lua-compatible string.
func luaEncodeElement(elem any) string {
	switch t := elem.(type) {
	case map[any]any:
		res := "{"
		first := true
		for k, v := range t {
			if !first {
				res += ", "
			}
			if str, ok := k.(string); ok {
				res += fmt.Sprintf("%s = %s", str, luaEncodeElement(v))
			} else {
				res += fmt.Sprintf("[%v] = %s", k, luaEncodeElement(v))
			}
			first = false
		}
		return res + "}"
	case []any:
		res := "{"
		for k, v := range t {
			res += luaEncodeElement(v)
			if k < len(t)-1 {
				res += ", "
			}
		}
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
	if err := yaml.Unmarshal([]byte(input), &decoded); err == nil {
		var res string
		for i, unpackedVal := range decoded {
			if i < len(decoded)-1 {
				res += luaEncodeElement(unpackedVal) + ", "
			} else {
				res += luaEncodeElement(unpackedVal)
			}
		}
		return res + ";\n", nil
	} else {
		return "", fmt.Errorf("cannot render lua: %w", err)
	}
}
