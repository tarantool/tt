package manifest

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var (
	// errComponentBlock reports a component AppendComponent cannot write.
	errComponentBlock = errors.New("cannot declare the component")
	// errBlockValue reports a value of a field AppendComponent cannot write.
	errBlockValue = errors.New("not a value of a manifest")
)

// ComponentBlock is a component to declare in a manifest (AppendComponent):
// the table [components.<Name>] and, with Build, its build block.
type ComponentBlock struct {
	// Name is the name of the component. Required.
	Name string
	// Comment, when not empty, is written above the table, a "# " line for
	// each of its lines.
	Comment string
	// Path is the directory of the component, relative to the project.
	// Required.
	Path string
	// Include is the include of the component; none writes none.
	Include []string
	// Build are the fields of its build block, [components.<Name>.build],
	// in the order they are written; none writes no build block.
	Build []BlockField
}

// BlockField is a field of a table AppendComponent and EditComponent write:
// a key and a value of a string, a bool, an integer, a slice of them or a
// map with string keys of them, a table, such as
// []map[string]string{{"path": "dist/calc.wasm", "to": "share"}}.
type BlockField struct {
	Key   string
	Value any
}

// AppendComponent returns src, the source of a manifest, with block appended
// to it as a table of its own after a blank line, every line and comment of
// src kept, in the line ending src uses:
//
//	# <comment>
//	[components.<name>]
//	path = "<path>"
//	include = ["<include>", ...]
//
//	[components.<name>.build]
//	<key> = <value>
//	...
//
// It neither parses nor checks the manifest it makes: whoever writes it
// does (ParseManifest, Validate). A block without a name or a path, a value
// of another type and a string that is not UTF-8 are errors.
func AppendComponent(src []byte, block ComponentBlock) ([]byte, error) {
	if block.Name == "" || block.Path == "" {
		return nil, fmt.Errorf("%w %q: it needs a name and a path", errComponentBlock, block.Name)
	}

	newline := detectNewline(src)
	table := "components." + quoteTOMLKey(block.Name)

	lines := []string{""}

	if block.Comment != "" {
		for line := range strings.SplitSeq(block.Comment, "\n") {
			lines = append(lines, strings.TrimRight("# "+line, " "))
		}
	}

	path, err := blockValue(block.Path)
	if err != nil {
		return nil, fmt.Errorf("%w %q: path: %w", errComponentBlock, block.Name, err)
	}

	lines = append(lines, "["+table+"]", "path = "+path)

	if len(block.Include) > 0 {
		include, err := blockValue(block.Include)
		if err != nil {
			return nil, fmt.Errorf("%w %q: include: %w", errComponentBlock, block.Name, err)
		}

		lines = append(lines, "include = "+include)
	}

	if len(block.Build) > 0 {
		lines = append(lines, "", "["+table+".build]")

		for _, field := range block.Build {
			if field.Key == "" {
				return nil, fmt.Errorf("%w %q: a field of its build block has no key",
					errComponentBlock, block.Name)
			}

			value, err := blockValue(field.Value)
			if err != nil {
				return nil, fmt.Errorf("%w %q: build.%s: %w", errComponentBlock, block.Name,
					field.Key, err)
			}

			lines = append(lines, quoteTOMLKey(field.Key)+" = "+value)
		}
	}

	out := append(make([]byte, 0, len(src)+len(newline)), src...)

	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, newline...)
	}

	for _, line := range lines {
		out = append(out, line...)
		out = append(out, newline...)
	}

	return out, nil
}

// blockValue renders value as a TOML value, on one line: a string, a bool
// and an integer as themselves, a slice as an array of its elements and a
// map with string keys as an inline table of its entries, in the order of
// their keys, whatever their types are named (a []string, a
// map[string]string, a []map[string]any).
func blockValue(value any) (string, error) {
	return renderValue(reflect.ValueOf(value))
}

// renderValue renders value as blockValue does.
func renderValue(value reflect.Value) (string, error) {
	if value.Kind() == reflect.Interface && !value.IsNil() {
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.String:
		str := value.String()
		if !utf8.ValidString(str) {
			return "", fmt.Errorf("%w: %q is not UTF-8", errBlockValue, str)
		}

		return quoteTOMLString(str), nil
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(value.Uint(), 10), nil
	case reflect.Slice, reflect.Array:
		items := make([]string, 0, value.Len())

		for i := range value.Len() {
			item, err := renderValue(value.Index(i))
			if err != nil {
				return "", err
			}

			items = append(items, item)
		}

		return "[" + strings.Join(items, ", ") + "]", nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			break
		}

		keys := make([]string, 0, value.Len())
		for _, key := range value.MapKeys() {
			keys = append(keys, key.String())
		}

		slices.Sort(keys)

		entries := make([]string, 0, len(keys))

		for _, key := range keys {
			item, err := renderValue(value.MapIndex(reflect.ValueOf(key).Convert(
				value.Type().Key())))
			if err != nil {
				return "", err
			}

			entries = append(entries, quoteTOMLKey(key)+" = "+item)
		}

		if len(entries) == 0 {
			return "{}", nil
		}

		return "{ " + strings.Join(entries, ", ") + " }", nil
	default:
	}

	return "", fmt.Errorf("%w: %s; a string, a bool, an integer, a slice or a map with "+
		"string keys of them is", errBlockValue, describeKind(value))
}

// describeKind names the Go type of value for an error of blockValue.
func describeKind(value reflect.Value) string {
	if !value.IsValid() {
		return "nil"
	}

	return value.Type().String()
}
