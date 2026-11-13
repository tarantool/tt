package formatter

import (
	"errors"
	"fmt"
	"reflect"
	"slices"

	yamlv2 "go.yaml.in/yaml/v2"
	"go.yaml.in/yaml/v3"
)

// The short tags of the scalars DecodeYAML reads apart from the library.
const (
	yamlBoolTag      = "!!bool"
	yamlMergeTag     = "!!merge"
	yamlNullTag      = "!!null"
	yamlTimestampTag = "!!timestamp"
)

// yamlQuotedStyles are the styles of a scalar that is not a plain one.
const yamlQuotedStyles = yaml.TaggedStyle | yaml.SingleQuotedStyle | yaml.DoubleQuotedStyle |
	yaml.LiteralStyle | yaml.FoldedStyle

var (
	errYAMLTarget = errors.New("yaml: DecodeYAML decodes into *any, *[]any or *map[string]any")
	errYAMLMerge  = errors.New("yaml: map merge requires map or sequence of maps as the value")
	errYAMLMapKey = errors.New("yaml: invalid map key")
	errYAMLAnchor = errors.New("yaml: anchor")
	errYAMLKind   = errors.New("yaml: unexpected node")
)

// yaml11Bool returns the boolean YAML 1.1 reads text as, if it reads it as
// one. YAML 1.2 reads all of them as strings but true and false.
func yaml11Bool(text string) (bool, bool) {
	switch text {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON", "true", "True", "TRUE":
		return true, true
	case "n", "N", "no", "No", "NO", "off", "Off", "OFF", "false", "False", "FALSE":
		return false, true
	default:
		return false, false
	}
}

// DecodeYAML decodes the first YAML document of data into out, a *any, a
// *[]any or a *map[string]any, as tt has always read the YAML a Tarantool
// console answers with, which is how gopkg.in/yaml.v2 read it:
//
//   - a map is a map[any]any whatever its keys, but for the keys of the
//     *map[string]any itself, which are the text of the key scalars;
//   - the YAML 1.1 booleans - y, yes, on, n, no, off and their capitalized
//     forms - are booleans when plain or tagged !!bool;
//   - a timestamp is the text it is written as, not a time.Time;
//   - a repeated key and a merge key overwrite the value of the keys before
//     them.
//
// Everything else - numbers, nulls, strings, !!binary, anchors - is read by
// go.yaml.in/yaml/v3.
func DecodeYAML(data []byte, out any) error {
	root, err := parseYAML(data)
	if err != nil {
		return err
	}

	switch target := out.(type) {
	case *any:
		value, err := newYAMLReader().value(root)
		if err != nil {
			return err
		}

		*target = value

		return nil
	case *[]any:
		return decodeYAMLSequence(root, target)
	case *map[string]any:
		return decodeYAMLMap(root, target)
	default:
		return fmt.Errorf("%w, not %T", errYAMLTarget, out)
	}
}

// EncodeYAML encodes value as block YAML the way tt's consoles have always
// printed it: an indent of two, a sequence in a map at the indent of its key,
// and a line longer than 80 columns folded at a space.
//
// It is the one place tt encodes with go.yaml.in/yaml/v2, the maintained
// successor of gopkg.in/yaml.v2, which tt printed this YAML with: the
// encoder of go.yaml.in/yaml/v3 has no line width and folds no line.
func EncodeYAML(value any) ([]byte, error) {
	data, err := yamlv2.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	return data, nil
}

// parseYAML parses the first document of data. It returns the node the
// document holds, a null for no document.
func parseYAML(data []byte) (*yaml.Node, error) {
	var doc yaml.Node

	err := yaml.Unmarshal(data, &doc)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlNullTag}, nil
	}

	return doc.Content[0], nil
}

// isYAMLNull reports whether node is a null scalar.
func isYAMLNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.ShortTag() == yamlNullTag
}

// yamlSequence returns the items of the sequence root, none for a null, and
// for anything else the error of decoding it into a slice.
func yamlSequence(root *yaml.Node) ([]*yaml.Node, error) {
	if isYAMLNull(root) {
		return nil, nil
	}

	if root.Kind != yaml.SequenceNode {
		var items []any

		return nil, kindError(root, &items)
	}

	return root.Content, nil
}

// kindError returns the error of decoding node, which is of another kind,
// into out: the library's, which names the line, the tag and the type. The
// node is decoded without its content, so that nothing in it, a repeated
// key, makes the error another one, and a map or a sequence is named by its
// kind whatever its tag.
func kindError(node *yaml.Node, out any) error {
	bare := *node

	bare.Content = nil
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		bare.Tag = ""
	}

	err := bare.Decode(out)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	return fmt.Errorf("%w: %s", errYAMLKind, node.ShortTag())
}

// decodeYAMLSequence decodes root, a sequence, into out.
func decodeYAMLSequence(root *yaml.Node, out *[]any) error {
	if isYAMLNull(root) {
		*out = nil

		return nil
	}

	items, err := yamlSequence(root)
	if err != nil {
		return err
	}

	reader := newYAMLReader()

	values := make([]any, len(items))
	for i, item := range items {
		values[i], err = reader.value(item)
		if err != nil {
			return err
		}
	}

	*out = values

	return nil
}

// decodeYAMLMap decodes root, a map, into out, the keys of it as their text.
func decodeYAMLMap(root *yaml.Node, out *map[string]any) error {
	if isYAMLNull(root) {
		*out = nil

		return nil
	}

	if root.Kind != yaml.MappingNode {
		var values map[string]any

		return kindError(root, &values)
	}

	values := map[string]any{}

	// A key that is no scalar is a type error, as decoding it into a string
	// is: the pair is skipped, and the errors of all such keys are returned
	// with the map of the others.
	var typeErrors []string

	err := newYAMLReader().mapping(root, func(key *yaml.Node, value any) error {
		if key.Kind == yaml.AliasNode {
			key = key.Alias
		}

		if key.Kind != yaml.ScalarNode {
			var (
				text    string
				typeErr *yaml.TypeError
			)

			err := kindError(key, &text)
			if !errors.As(err, &typeErr) {
				return err
			}

			typeErrors = append(typeErrors, typeErr.Errors...)

			return nil
		}

		text := key.Value
		if key.ShortTag() == yamlNullTag {
			text = ""
		}

		values[text] = value

		return nil
	})
	if err != nil {
		return err
	}

	*out = values

	if len(typeErrors) > 0 {
		return &yaml.TypeError{Errors: typeErrors}
	}

	return nil
}

// yamlReader turns nodes into values. It keeps the anchors being read, to
// refuse an alias inside the value of its own anchor.
type yamlReader struct {
	reading map[*yaml.Node]bool
}

func newYAMLReader() *yamlReader {
	return &yamlReader{reading: map[*yaml.Node]bool{}}
}

// value returns the value of node, a node of a document.
func (r *yamlReader) value(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.AliasNode:
		return r.alias(node)
	case yaml.SequenceNode:
		values := make([]any, len(node.Content))

		for index, item := range node.Content {
			value, err := r.value(item)
			if err != nil {
				return nil, err
			}

			values[index] = value
		}

		return values, nil
	case yaml.MappingNode:
		values := map[any]any{}

		err := r.mapping(node, func(key *yaml.Node, value any) error {
			keyValue, err := r.value(key)
			if err != nil {
				return err
			}

			if keyValue != nil && !reflect.TypeOf(keyValue).Comparable() {
				return fmt.Errorf("%w: %#v", errYAMLMapKey, keyValue)
			}

			values[keyValue] = value

			return nil
		})
		if err != nil {
			return nil, err
		}

		return values, nil
	case yaml.ScalarNode:
		return scalarValue(node)
	default:
		return nil, fmt.Errorf("%w: kind %d", errYAMLKind, node.Kind)
	}
}

// alias returns the value of the node an alias names.
func (r *yamlReader) alias(node *yaml.Node) (any, error) {
	if r.reading[node.Alias] {
		return nil, fmt.Errorf("%w '%s' value contains itself", errYAMLAnchor, node.Value)
	}

	r.reading[node.Alias] = true
	defer delete(r.reading, node.Alias)

	return r.value(node.Alias)
}

// mapping reads the pairs of the map node in order and puts each through
// put, the pairs of a merged map in place of its merge key.
func (r *yamlReader) mapping(node *yaml.Node, put func(key *yaml.Node, value any) error) error {
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]

		if key.Kind == yaml.ScalarNode && key.Value == "<<" && key.ShortTag() == yamlMergeTag {
			err := r.merge(value, put)
			if err != nil {
				return err
			}

			continue
		}

		decoded, err := r.value(value)
		if err != nil {
			return err
		}

		err = put(key, decoded)
		if err != nil {
			return err
		}
	}

	return nil
}

// merge puts the pairs of the value of a merge key through put: a map, an
// alias of one, or a sequence of them, the first of which wins.
func (r *yamlReader) merge(node *yaml.Node, put func(key *yaml.Node, value any) error) error {
	switch node.Kind {
	case yaml.MappingNode:
		return r.mapping(node, put)
	case yaml.AliasNode:
		if node.Alias.Kind != yaml.MappingNode {
			return errYAMLMerge
		}

		return r.mapping(node.Alias, put)
	case yaml.SequenceNode:
		for _, item := range slices.Backward(node.Content) {
			if item.Kind == yaml.AliasNode {
				item = item.Alias
			}

			if item.Kind != yaml.MappingNode {
				return errYAMLMerge
			}

			err := r.mapping(item, put)
			if err != nil {
				return err
			}
		}

		return nil
	default:
		return errYAMLMerge
	}
}

// scalarValue returns the value of a scalar node.
func scalarValue(node *yaml.Node) (any, error) {
	tag := node.ShortTag()

	if tag == yamlTimestampTag {
		return node.Value, nil
	}

	if node.Style&yamlQuotedStyles == 0 || tag == yamlBoolTag {
		if value, ok := yaml11Bool(node.Value); ok {
			return value, nil
		}
	}

	var value any

	err := node.Decode(&value)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}

	return value, nil
}
