package tuple

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// AppendJSON appends the JSON of value to dst. The package documentation
// describes the mapping; a value of a type it does not cover is written by
// encoding/json.
//
// A string that is not valid UTF-8 has each invalid byte replaced by U+FFFD,
// as encoding/json does: JSON text is Unicode. On an error dst is returned as
// it is.
func AppendJSON(dst []byte, value any) ([]byte, error) {
	out, err := appendJSON(dst, value)
	if err != nil {
		return dst, err
	}

	return out, nil
}

//nolint:cyclop // One case per type is the clearest form.
func appendJSON(dst []byte, value any) ([]byte, error) {
	switch typed := value.(type) {
	case nil:
		return append(dst, "null"...), nil
	case bool:
		return strconv.AppendBool(dst, typed), nil
	case string:
		return appendJSONString(dst, typed), nil
	case float32:
		return appendJSONFloat(dst, float64(typed), float32Bits), nil
	case float64:
		return appendJSONFloat(dst, typed, float64Bits), nil
	case []any:
		return appendJSONArray(dst, typed)
	case Map:
		return appendJSONMap(dst, typed)
	case map[any]any:
		return appendJSONMap(dst, sortedMap(typed))
	case map[string]any:
		items := make(Map, 0, len(typed))
		for key, item := range typed {
			items = append(items, MapItem{Key: key, Value: item})
		}

		return appendJSONMap(dst, sortItems(items))
	}

	if i, ok := asInt(value); ok {
		return strconv.AppendInt(dst, i, decimalBase), nil
	}

	if u, ok := asUint(value); ok {
		return strconv.AppendUint(dst, u, decimalBase), nil
	}

	if text, ok := Text(value); ok {
		return appendJSONString(dst, text), nil
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return dst, fmt.Errorf("encoding %T as JSON: %w", value, err)
	}

	return append(dst, encoded...), nil
}

func appendJSONFloat(dst []byte, number float64, bitSize int) []byte {
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return appendJSONString(dst, floatText(number, bitSize))
	}

	return append(dst, formatFloat(number, bitSize)...)
}

func appendJSONArray(dst []byte, items []any) ([]byte, error) {
	dst = append(dst, '[')

	for i, item := range items {
		if i > 0 {
			dst = append(dst, ',')
		}

		var err error

		dst, err = appendJSON(dst, item)
		if err != nil {
			return dst, err
		}
	}

	return append(dst, ']'), nil
}

func appendJSONMap(dst []byte, items Map) ([]byte, error) {
	dst = append(dst, '{')

	for i, item := range items {
		if i > 0 {
			dst = append(dst, ',')
		}

		dst = appendJSONString(dst, keyText(item.Key))
		dst = append(dst, ':')

		var err error

		dst, err = appendJSON(dst, item.Value)
		if err != nil {
			return dst, err
		}
	}

	return append(dst, '}'), nil
}

// keyText is the text a map key has as a JSON object key: its Text, or for a
// key with none - nil, an array, a map - its JSON.
func keyText(key any) string {
	if text, ok := Text(key); ok {
		return text
	}

	encoded, err := AppendJSON(nil, key)
	if err != nil {
		return fmt.Sprint(key)
	}

	return string(encoded)
}

// sortedMap returns the entries of m ordered by the text of their keys.
func sortedMap(m map[any]any) Map {
	items := make(Map, 0, len(m))
	for key, value := range m {
		items = append(items, MapItem{Key: key, Value: value})
	}

	return sortItems(items)
}

// sortItems orders the entries of a Go map, which has no order, by the text
// of their keys, then, for keys of the same text, such as 1 and "1", by the
// Go types of the keys and by the tagged JSON of the entries, so that a map
// is always written the same.
func sortItems(items Map) Map {
	slices.SortFunc(items, func(left, right MapItem) int {
		order := strings.Compare(keyText(left.Key), keyText(right.Key))
		if order != 0 {
			return order
		}

		order = strings.Compare(fmt.Sprintf("%T", left.Key), fmt.Sprintf("%T", right.Key))
		if order != 0 {
			return order
		}

		return strings.Compare(entryText(left), entryText(right))
	})

	return items
}

// entryText is the tagged JSON of an entry of a map, which tells entries
// apart where their keys alone do not.
func entryText(item MapItem) string {
	text, _ := appendTaggedJSON(nil, []any{item.Key, item.Value})

	return string(text)
}

const hexDigits = "0123456789abcdef"

// appendJSONString appends text as a JSON string.
func appendJSONString(dst []byte, text string) []byte {
	dst = append(dst, '"')

	for pos := 0; pos < len(text); {
		char := text[pos]
		if char < utf8.RuneSelf {
			switch {
			case char == '"' || char == '\\':
				dst = append(dst, '\\', char)
			case char == '\n':
				dst = append(dst, '\\', 'n')
			case char == '\r':
				dst = append(dst, '\\', 'r')
			case char == '\t':
				dst = append(dst, '\\', 't')
			case char < 0x20 || char == 0x7f:
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[char>>4], hexDigits[char&0xf])
			default:
				dst = append(dst, char)
			}

			pos++

			continue
		}

		symbol, size := utf8.DecodeRuneInString(text[pos:])

		switch {
		case symbol == utf8.RuneError && size == 1:
			dst = append(dst, "\\ufffd"...)
		case symbol == 0x2028 || symbol == 0x2029:
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[symbol&0xf])
		default:
			dst = append(dst, text[pos:pos+size]...)
		}

		pos += size
	}

	return append(dst, '"')
}

// MarshalJSON renders the map as AppendJSON does.
func (m Map) MarshalJSON() ([]byte, error) {
	return AppendJSON(nil, m)
}

// MarshalJSON renders the value as its base64.
func (b Binary) MarshalJSON() ([]byte, error) {
	return appendJSONString(nil, base64.StdEncoding.EncodeToString(b)), nil
}

// MarshalJSON renders the extension as its Text.
func (e Ext) MarshalJSON() ([]byte, error) {
	text, _ := Text(e)

	return appendJSONString(nil, text), nil
}

// Value wraps a decoded value so that encoding/json and gopkg.in/yaml.v3
// render it as AppendJSON and YAMLNode do, wherever a struct holds it.
type Value struct {
	V any
}

// MarshalJSON renders the value as AppendJSON does.
func (v Value) MarshalJSON() ([]byte, error) {
	return AppendJSON(nil, v.V)
}
