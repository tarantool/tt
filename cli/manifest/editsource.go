package manifest

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// This file is the locating half of the manifest editor: it turns TOML source
// text into byte ranges the splicing in edit.go can act on. Nothing here
// mutates anything.

// depthEntry is the length of the key path of a dependency declared directly
// as a key of its table ("dependencies", "luasocket"); depthField is the
// length of the path of one of its long-form fields ("dependencies",
// "luasocket", "version").
const (
	depthEntry = 2
	depthField = 3
)

// versionKey is the long-form field a version constraint lives in, and the
// only field this editor ever rewrites.
const versionKey = "version"

// span is a half-open byte range [start, end) of a document's source.
type span struct {
	start int
	end   int
}

// tomlExpr is one top-level TOML expression located in the source.
//
// The unstable parser reuses its node storage between expressions, so nothing
// here may hold *unstable.Node: every field is a plain copy taken while the
// expression was the current one.
type tomlExpr struct {
	kind unstable.Kind // Comment, Table, ArrayTable or KeyValue.
	// path is the fully qualified key path: the enclosing table's path
	// followed by the expression's own key. Nil for a comment.
	path []string
	// table is the path of the table header this expression sits under, nil at
	// the document root. For a header it is the header's own path.
	table []string
	// start is the offset of the expression's first byte - the '[' of a table
	// header, the first key character of a key/value, the '#' of a comment.
	start int
	// end is the offset just past its last non-blank byte, and so covers a
	// trailing same-line comment, which the parser chains onto the expression
	// rather than reporting separately.
	end int

	valueKind unstable.Kind // Kind of a key/value's value node.
	value     span          // Raw token range of that value; quotes included.
	valueData string        // Its decoded text, for a scalar.

	// hasVersion and the two fields after it describe the version key of an
	// inline-table value. Only a string-valued version counts: rewriting
	// anything else would change the value's type.
	hasVersion  bool
	version     span
	versionData string
}

// document is an indexed snapshot of one TOML source: its expressions in
// source order plus the two whitespace conventions any inserted line has to
// match.
type document struct {
	src             []byte
	exprs           []tomlExpr
	newline         string // "\n" or "\r\n", taken from the first line break.
	trailingNewline bool   // Whether the source ends with a line break.
}

// indexSource parses src and records where every top-level expression starts
// and ends.
//
// Comments are parsed as expressions (KeepComments) purely for their offsets:
// they are what bounds the expression before them, so a comment line written
// above an entry is not swallowed by the entry above it.
func indexSource(src []byte) (*document, error) {
	var parser unstable.Parser

	parser.KeepComments = true
	parser.Reset(src)

	var (
		exprs []tomlExpr
		table []string
	)

	for parser.NextExpression() {
		item, err := describeExpr(parser.Expression(), src, table)
		if err != nil {
			return nil, err
		}

		if item.kind == unstable.Table || item.kind == unstable.ArrayTable {
			table = item.path
		}

		exprs = append(exprs, item)
	}

	err := parser.Error()
	if err != nil {
		return nil, fmt.Errorf("parsing manifest for editing: %w", err)
	}

	// An expression's end is only knowable once the next one is known: the
	// parser reports where tokens are, not where an expression stops. The gap
	// between two consecutive expressions is whitespace, so the end is the
	// right-trimmed run up to the next start (or to EOF for the last one).
	for idx := range exprs {
		limit := len(src)
		if idx+1 < len(exprs) {
			limit = exprs[idx+1].start
		}

		exprs[idx].end = trimBlankSuffix(src, exprs[idx].start, limit)
	}

	doc := &document{
		src:             src,
		exprs:           exprs,
		newline:         detectNewline(src),
		trailingNewline: len(src) > 0 && src[len(src)-1] == '\n',
	}

	return doc, nil
}

// describeExpr copies out of one expression node everything the editor needs,
// while that node is still the parser's current one.
func describeExpr(node *unstable.Node, src []byte, table []string) (tomlExpr, error) {
	var item tomlExpr

	item.kind = node.Kind

	switch node.Kind {
	case unstable.Comment:
		item.start = int(node.Raw.Offset)
	case unstable.Table, unstable.ArrayTable:
		// A Table node carries no Raw of its own - only its key children do -
		// so the header's start is found by walking back from the first key
		// over the whitespace and brackets that must precede it.
		path, first := keyPath(node)

		item.path = path
		item.table = path
		item.start = scanBackToBracket(src, first)
	case unstable.KeyValue:
		path, first := keyPath(node)

		item.start = first
		item.table = table
		item.path = append(append([]string(nil), table...), path...)

		describeValue(&item, node.Value())
	default:
		return item, fmt.Errorf("%w: unexpected %s expression at byte %d",
			ErrUnsupportedForm, node.Kind, node.Raw.Offset)
	}

	return item, nil
}

// keyPath returns the decoded key path of a table header or key/value and the
// offset of its first key token. Quoted keys are decoded, so a name is matched
// as it is meant rather than as it is spelled.
func keyPath(node *unstable.Node) ([]string, int) {
	var path []string

	first := 0
	iter := node.Key()

	for iter.Next() {
		key := iter.Node()
		if path == nil {
			first = int(key.Raw.Offset)
		}

		path = append(path, string(key.Data))
	}

	return path, first
}

// describeValue records a key/value's value token and, for an inline table,
// the token of its version field.
func describeValue(item *tomlExpr, value *unstable.Node) {
	item.valueKind = value.Kind
	item.value = spanOf(value)
	item.valueData = string(value.Data)

	if value.Kind != unstable.InlineTable {
		return
	}

	// An inline table's own Raw covers just the opening brace, so its extent
	// is never used; what is needed is inside it.
	iter := value.Children()
	for iter.Next() {
		entry := iter.Node()

		path, _ := keyPath(entry)
		if len(path) != 1 || path[0] != versionKey {
			continue
		}

		field := entry.Value()
		if field.Kind != unstable.String {
			continue
		}

		item.hasVersion = true
		item.version = spanOf(field)
		item.versionData = string(field.Data)
	}
}

// spanOf converts a node's raw token range into a span.
func spanOf(node *unstable.Node) span {
	return span{
		start: int(node.Raw.Offset),
		end:   int(node.Raw.Offset) + int(node.Raw.Length),
	}
}

// scanBackToBracket walks back from a table header's first key token over the
// whitespace and the one or two '[' that TOML allows there, and returns the
// offset of the first bracket.
func scanBackToBracket(src []byte, keyStart int) int {
	pos := keyStart
	for pos > 0 && (src[pos-1] == ' ' || src[pos-1] == '\t') {
		pos--
	}

	for pos > 0 && src[pos-1] == '[' {
		pos--
	}

	return pos
}

// trimBlankSuffix returns the offset just past the last non-blank byte of
// src[start:limit].
func trimBlankSuffix(src []byte, start, limit int) int {
	end := limit
	for end > start && isBlankByte(src[end-1]) {
		end--
	}

	return end
}

// isBlankByte reports whether b is whitespace or a line break.
func isBlankByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

// detectNewline reports the line ending the source already uses, so an
// inserted line does not mix conventions into a CRLF file.
func detectNewline(src []byte) string {
	idx := bytes.IndexByte(src, '\n')
	if idx > 0 && src[idx-1] == '\r' {
		return "\r\n"
	}

	return "\n"
}

// lineStart returns the offset of the first byte of the line containing pos.
func (d *document) lineStart(pos int) int {
	return bytes.LastIndexByte(d.src[:pos], '\n') + 1
}

// lineEnd returns the offset just past the line break that ends the line
// containing pos, or the end of the source when that line is unterminated.
func (d *document) lineEnd(pos int) int {
	idx := bytes.IndexByte(d.src[pos:], '\n')
	if idx < 0 {
		return len(d.src)
	}

	return pos + idx + 1
}

// declares reports whether the table declares name in any form, editable or
// not.
func (d *document) declares(table DepTable, name string) bool {
	for i := range d.exprs {
		if hasDepPrefix(d.exprs[i].path, table, name) {
			return true
		}
	}

	return false
}

// hasDepPrefix reports whether a key path belongs to the given dependency:
// either it is the declaration itself ("dependencies", "luasocket") or one of
// its fields below that.
func hasDepPrefix(path []string, table DepTable, name string) bool {
	return len(path) >= depthEntry && path[0] == string(table) && path[1] == name
}

// quoteTOMLString renders s as a TOML basic string, escaping what the format
// requires. Everything a constraint can contain is representable this way, so
// there is no value SetDependency has to refuse for being unwritable.
func quoteTOMLString(str string) string {
	var out strings.Builder

	out.WriteByte('"')

	for _, char := range str {
		switch char {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\b':
			out.WriteString(`\b`)
		case '\t':
			out.WriteString(`\t`)
		case '\n':
			out.WriteString(`\n`)
		case '\f':
			out.WriteString(`\f`)
		case '\r':
			out.WriteString(`\r`)
		default:
			if char < ' ' || char == 0x7f {
				fmt.Fprintf(&out, `\u%04X`, char)
			} else {
				out.WriteRune(char)
			}
		}
	}

	out.WriteByte('"')

	return out.String()
}

// quoteTOMLKey renders name as a TOML key, quoting it only when it is not a
// bare key. Package names are [a-z][a-z0-9-]* and so never need quoting, but
// the editor is not the place to assume the manifest already validates.
func quoteTOMLKey(name string) string {
	for i := range len(name) {
		char := name[i]

		bare := char >= 'A' && char <= 'Z' ||
			char >= 'a' && char <= 'z' ||
			char >= '0' && char <= '9' ||
			char == '-' || char == '_'
		if !bare {
			return quoteTOMLString(name)
		}
	}

	if name == "" {
		return quoteTOMLString(name)
	}

	return name
}

// formatEntry renders a short-form dependency line.
func formatEntry(name, constraint string) string {
	return quoteTOMLKey(name) + " = " + quoteTOMLString(constraint)
}
