package tuple

import (
	"encoding/base64"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// YAML tags of the nodes YAMLNode builds.
const (
	yamlNull   = "!!null"
	yamlBool   = "!!bool"
	yamlInt    = "!!int"
	yamlFloat  = "!!float"
	yamlStr    = "!!str"
	yamlBinary = "!!binary"
)

// YAMLNode returns the YAML node of value. Arrays and maps are in flow style,
// as Tarantool prints a tuple: [1, 'a', {b: 2}]. A float that is NaN or
// infinite is .nan, .inf or -.inf, a [Binary] or a []byte a !!binary scalar
// of its base64, an extension and any other scalar a string of its [Text].
// A string that is not valid UTF-8 has each invalid byte replaced by U+FFFD,
// as in [AppendJSON], since YAML is Unicode, and one YAML 1.1 - the YAML of
// Tarantool - reads as a bool, such as yes or off, or Tarantool reads as a
// number, such as nan, Infinity or 0x1p4, is quoted. A value of a type the
// package does not cover is left to yaml.v3.
//
//nolint:cyclop // One case per type is the clearest form.
func YAMLNode(value any) *yaml.Node {
	switch typed := value.(type) {
	case nil:
		return scalar(yamlNull, "null")
	case bool:
		return scalar(yamlBool, strconv.FormatBool(typed))
	case string:
		return yamlString(typed)
	case Binary:
		return scalar(yamlBinary, base64.StdEncoding.EncodeToString(typed))
	case []byte:
		return scalar(yamlBinary, base64.StdEncoding.EncodeToString(typed))
	case float32:
		return yamlFloatNode(float64(typed), float32Bits)
	case float64:
		return yamlFloatNode(typed, float64Bits)
	case []any:
		node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		for _, item := range typed {
			node.Content = append(node.Content, YAMLNode(item))
		}

		return node
	case Map:
		return yamlMap(typed)
	case map[any]any:
		return yamlMap(sortedMap(typed))
	case map[string]any:
		items := make(Map, 0, len(typed))
		for key, item := range typed {
			items = append(items, MapItem{Key: key, Value: item})
		}

		return yamlMap(sortItems(items))
	}

	if i, ok := asInt(value); ok {
		return scalar(yamlInt, strconv.FormatInt(i, 10))
	}

	if u, ok := asUint(value); ok {
		return scalar(yamlInt, strconv.FormatUint(u, 10))
	}

	if text, ok := Text(value); ok {
		return scalar(yamlStr, text)
	}

	node := &yaml.Node{}

	err := node.Encode(value)
	if err != nil {
		return scalar(yamlStr, err.Error())
	}

	return node
}

func scalar(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

// yaml11Bools are the words YAML 1.1 reads as a bool and YAML 1.2, which
// yaml.v3 writes, as a string it leaves unquoted.
var yaml11Bools = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true,
	"n": true, "N": true, "no": true, "No": true, "NO": true,
	"on": true, "On": true, "ON": true, "off": true, "Off": true, "OFF": true,
}

// cNumber matches the text the strtod of C reads whole, the test Tarantool
// reads a plain scalar of YAML as a number by: a decimal or a hexadecimal
// float, inf, infinity or nan, in any case. yaml.v3 quotes only those of them
// YAML 1.2 reads as a number.
var cNumber = regexp.MustCompile(`^[+-]?(?i:` +
	`(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:e[+-]?[0-9]+)?|` +
	`0x(?:[0-9a-f]+\.?[0-9a-f]*|\.[0-9a-f]+)(?:p[+-]?[0-9]+)?|` +
	`inf(?:inity)?|nan(?:\([0-9a-z_]*\))?)$`)

// yamlMergeKey is the merge key of YAML, which yaml.v3 does not quote.
const yamlMergeKey = "<<"

// yamlString is the node of a string: its invalid bytes replaced by U+FFFD,
// and double-quoted if YAML 1.1 would read it as a bool or the merge key,
// Tarantool as a number, or it has a control character, which yaml.v3 would
// put in a block that does not always read back: "\n" or "\t\n".
func yamlString(text string) *yaml.Node {
	if !utf8.ValidString(text) {
		valid := make([]byte, 0, len(text))

		for pos := 0; pos < len(text); {
			symbol, size := utf8.DecodeRuneInString(text[pos:])

			valid = utf8.AppendRune(valid, symbol)

			pos += size
		}

		text = string(valid)
	}

	node := scalar(yamlStr, text)
	if yaml11Bools[text] || text == yamlMergeKey || cNumber.MatchString(text) ||
		strings.ContainsFunc(text, unicode.IsControl) {
		node.Style = yaml.DoubleQuotedStyle
	}

	return node
}

func yamlFloatNode(number float64, bitSize int) *yaml.Node {
	switch {
	case math.IsNaN(number):
		return scalar(yamlFloat, ".nan")
	case math.IsInf(number, 1):
		return scalar(yamlFloat, ".inf")
	case math.IsInf(number, -1):
		return scalar(yamlFloat, "-.inf")
	}

	return scalar(yamlFloat, floatLiteral(number, bitSize))
}

// floatLiteral is number as the shortest decimal that reads back as the same
// float, with ".0" added to an integral one, so that YAML and Lua read it as
// a float rather than as an integer.
func floatLiteral(number float64, bitSize int) string {
	text := formatFloat(number, bitSize)
	if strings.ContainsAny(text, ".e") {
		return text
	}

	return text + ".0"
}

func yamlMap(items Map) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
	for _, item := range items {
		node.Content = append(node.Content, YAMLNode(item.Key), YAMLNode(item.Value))
	}

	return node
}

// MarshalYAML renders the map as YAMLNode does.
func (m Map) MarshalYAML() (any, error) {
	return YAMLNode(m), nil
}

// MarshalYAML renders the value as a !!binary scalar.
func (b Binary) MarshalYAML() (any, error) {
	return YAMLNode(b), nil
}

// MarshalYAML renders the extension as its Text.
func (e Ext) MarshalYAML() (any, error) {
	return YAMLNode(e), nil
}

// MarshalYAML renders the value as YAMLNode does.
func (v Value) MarshalYAML() (any, error) {
	return YAMLNode(v.V), nil
}
