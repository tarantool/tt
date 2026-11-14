package manifest

import (
	"errors"
	"fmt"
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

// BlockField is a field of a table AppendComponent writes: a key and a
// value of a string, a bool, an int or a []string.
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

// blockValue renders value as a TOML value.
func blockValue(value any) (string, error) {
	switch value := value.(type) {
	case string:
		if !utf8.ValidString(value) {
			return "", fmt.Errorf("%w: %q is not UTF-8", errBlockValue, value)
		}

		return quoteTOMLString(value), nil
	case bool:
		return strconv.FormatBool(value), nil
	case int:
		return strconv.Itoa(value), nil
	case []string:
		items := make([]string, 0, len(value))

		for _, item := range value {
			rendered, err := blockValue(item)
			if err != nil {
				return "", err
			}

			items = append(items, rendered)
		}

		return "[" + strings.Join(items, ", ") + "]", nil
	default:
		return "", fmt.Errorf("%w: %T; a string, a bool, an int or a []string is", errBlockValue,
			value)
	}
}
