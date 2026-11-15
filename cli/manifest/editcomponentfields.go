package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// This file changes and removes a component a manifest declares, in its
// source text, as edit.go does a dependency: a value is replaced where it
// is written, a new key goes after the last key of its table, a removed key
// takes its lines with it, and every other byte stays.

// ErrNoComponent reports a component EditComponent and RemoveComponent are
// given that the source does not declare. Callers match it with errors.Is.
var ErrNoComponent = errors.New("the manifest declares no component")

// componentsKey is the table of the components, and buildKey the key of the
// build block of a component.
const (
	componentsKey = "components"
	buildKey      = "build"
)

// ComponentEdit is a change of a component a manifest declares
// (EditComponent).
type ComponentEdit struct {
	// Name is the name of the component. Required.
	Name string
	// Fields are fields of [components.<Name>] to write, such as path and
	// include, in order: a field there has its value replaced, another is
	// added after the last field of the table.
	Fields []BlockField
	// Build are fields of its build block to write, as Fields are; a
	// component without a build block gets one, [components.<Name>.build].
	Build []BlockField
	// Remove are the keys to remove, before anything is written: a field of
	// the component ("include"), a field of its build block ("build.cwd"),
	// or the build block ("build"), with its tables. A key the component
	// does not have is no error.
	Remove []string
}

// EditComponent returns src, the source of a manifest, with the component
// edit names changed as edit says, every other line and comment of src
// kept. A value is written in place of the one there, on one line, and the
// lines of a removed field go with it. It neither parses nor checks the
// manifest it makes: whoever writes it does (ParseManifest, Validate).
//
// A component src does not declare is ErrNoComponent. A field written in a
// form the editor does not follow - inside an inline table, such as
// build = { backend = "make" }, or as a table where a value is to go - is
// refused with ErrUnsupportedForm, naming it, to be edited by hand.
func EditComponent(src []byte, edit ComponentEdit) ([]byte, error) {
	table := []string{componentsKey, edit.Name}

	doc, err := indexSource(src)
	if err != nil {
		return nil, err
	}

	if !doc.hasPrefix(table) {
		return nil, fmt.Errorf("%w %q", ErrNoComponent, edit.Name)
	}

	out := src

	for _, key := range edit.Remove {
		path, err := componentKey(table, key)
		if err != nil {
			return nil, err
		}

		out, err = removeKey(out, path)
		if err != nil {
			return nil, err
		}
	}

	for _, set := range []struct {
		table  []string
		fields []BlockField
	}{
		{table, edit.Fields},
		{append(slices.Clone(table), buildKey), edit.Build},
	} {
		for _, field := range set.fields {
			if field.Key == "" || field.Key == buildKey && len(set.table) == len(table) {
				return nil, fmt.Errorf("%w %q: %q is not a field to write", errComponentBlock,
					edit.Name, field.Key)
			}

			value, err := blockValue(field.Value)
			if err != nil {
				return nil, fmt.Errorf("%w %q: %s: %w", errComponentBlock, edit.Name,
					joinKeys(append(slices.Clone(set.table[1:]), field.Key)), err)
			}

			out, err = setField(out, set.table, field.Key, value)
			if err != nil {
				return nil, err
			}
		}
	}

	return out, nil
}

// RemoveComponent returns src, the source of a manifest, without the
// component name: its tables, [components.<name>] and those below it, from
// their headers to their last keys, the blank lines above them, and the
// keys that declare it elsewhere, such as name = { ... } in [components].
// Comments above a table and after its last key stay, as RemoveDependency
// leaves them. A component src does not declare is ErrNoComponent. It
// neither parses nor checks the manifest it makes: a product that names the
// component is for Validate to refuse.
func RemoveComponent(src []byte, name string) ([]byte, error) {
	table := []string{componentsKey, name}

	doc, err := indexSource(src)
	if err != nil {
		return nil, err
	}

	if !doc.hasPrefix(table) {
		return nil, fmt.Errorf("%w %q", ErrNoComponent, name)
	}

	return doc.cut(doc.prefixSpans(table)), nil
}

// componentKey returns the key path of key, a key of ComponentEdit.Remove,
// in the component of table.
func componentKey(table []string, key string) ([]string, error) {
	parts := strings.Split(key, ".")

	switch {
	case slices.Contains(parts, ""):
	case len(parts) == 1:
		return append(slices.Clone(table), parts[0]), nil
	case len(parts) == 2 && parts[0] == buildKey:
		return append(slices.Clone(table), parts...), nil
	}

	return nil, fmt.Errorf("%w %q: %q is not a field of a component or of its build block",
		errComponentBlock, table[1], key)
}

// hasPrefix reports whether an expression of d declares something at or
// below the key path prefix.
func (d *document) hasPrefix(prefix []string) bool {
	return slices.ContainsFunc(d.exprs, func(item tomlExpr) bool {
		return pathHasPrefix(item.path, prefix)
	})
}

// pathHasPrefix reports whether path is prefix or below it.
func pathHasPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
}

// setField returns src with the field key of the table at the key path
// table set to value, rendered: replaced where the key is written, or
// added after the last key of the table, or in a new table when there is
// none.
func setField(src []byte, table []string, key, value string) ([]byte, error) {
	doc, err := indexSource(src)
	if err != nil {
		return nil, err
	}

	path := append(slices.Clone(table), key)

	for idx := range doc.exprs {
		item := &doc.exprs[idx]

		switch {
		case item.kind == unstable.KeyValue && slices.Equal(item.path, path):
			at, err := doc.valueSpan(item)
			if err != nil {
				return nil, err
			}

			return doc.replace(at, value), nil
		case pathHasPrefix(item.path, path):
			return nil, fmt.Errorf("%s: %w: it is written as a table, edit it by hand",
				joinKeys(path), ErrUnsupportedForm)
		case item.kind == unstable.KeyValue && pathHasPrefix(table, item.path):
			return nil, fmt.Errorf("%s: %w: it is written as an inline value of %s, edit it "+
				"by hand", joinKeys(path), ErrUnsupportedForm, joinKeys(item.path))
		}
	}

	anchor, prefix := doc.fieldAnchor(table)
	if anchor >= 0 {
		line := doc.indentOf(anchor) + joinKeys(append(prefix, key)) + " = " + value

		return doc.spliceLine(doc.lineEnd(doc.exprs[anchor].end), line), nil
	}

	return doc.insertTable(table, key+" = "+value), nil
}

// fieldAnchor returns the index of the expression of d a new field of the
// table at the key path table goes after, and the keys the field is written
// under there: the last key of the table under its header, with none, or
// the header itself; else the last dotted key that writes into the table,
// from a table above it, with the keys from that table down. -1 when the
// table is written nowhere.
func (d *document) fieldAnchor(table []string) (int, []string) {
	header := -1
	anchor := -1

	for idx := range d.exprs {
		item := &d.exprs[idx]

		switch {
		case item.kind == unstable.Table && slices.Equal(item.path, table):
			header = idx
		case item.kind == unstable.KeyValue && slices.Equal(item.table, table):
			header = max(header, idx)
		}
	}

	if header >= 0 {
		return header, nil
	}

	var prefix []string

	for idx := range d.exprs {
		item := &d.exprs[idx]
		if item.kind != unstable.KeyValue || !pathHasPrefix(item.path, table) ||
			len(item.table) >= len(table) || !pathHasPrefix(table, item.table) {
			continue
		}

		anchor = idx
		prefix = slices.Clone(table[len(item.table):])
	}

	return anchor, prefix
}

// insertTable returns the source of d with a new table at the key path
// table holding the one line entry: after the last key of the table the new
// one is below, under the header of that table, or at the end of the
// source when the table above has no header, so that the new header takes
// no key of another table under it.
func (d *document) insertTable(table []string, entry string) []byte {
	parent := table[:len(table)-1]
	anchor := -1

	for idx := range d.exprs {
		item := &d.exprs[idx]
		if item.kind == unstable.Table && slices.Equal(item.path, parent) ||
			item.kind == unstable.KeyValue && slices.Equal(item.table, parent) {
			anchor = idx
		}
	}

	header := "[" + joinKeys(table) + "]"

	if anchor < 0 {
		out := bytes.Clone(d.src)
		if len(out) > 0 && !d.trailingNewline {
			out = append(out, d.newline...)
		}

		return append(out, d.newline+header+d.newline+entry+d.newline...)
	}

	offset := d.lineEnd(d.exprs[anchor].end)
	out := make([]byte, 0, len(d.src)+len(header)+len(entry)+3*len(d.newline))

	out = append(out, d.src[:offset]...)

	if offset == len(d.src) && !d.trailingNewline {
		out = append(out, d.newline...)
	}

	out = append(out, d.newline+header+d.newline+entry+d.newline...)

	return append(out, d.src[offset:]...)
}

// removeKey returns src without the key at the key path path: the lines of
// its key/value, or, for a table, those of the table and of every table
// below it (prefixSpans). A key src does not write is no change; one inside
// an inline value is refused with ErrUnsupportedForm.
func removeKey(src []byte, path []string) ([]byte, error) {
	doc, err := indexSource(src)
	if err != nil {
		return nil, err
	}

	for idx := range doc.exprs {
		item := &doc.exprs[idx]
		if item.kind == unstable.KeyValue && len(item.path) < len(path) &&
			pathHasPrefix(path, item.path) {
			return nil, fmt.Errorf("%s: %w: it is written in an inline value of %s, edit it "+
				"by hand", joinKeys(path), ErrUnsupportedForm, joinKeys(item.path))
		}
	}

	if !doc.hasPrefix(path) {
		return src, nil
	}

	return doc.cut(doc.prefixSpans(path)), nil
}

// prefixSpans returns the byte ranges that declare the key path prefix and
// what is below it, ascending and never overlapping: each table at or below
// it from the blank lines above its header to the line of its last key, and
// each key/value at or below it written in another table, its lines.
func (d *document) prefixSpans(prefix []string) []span {
	var spans []span

	for idx := range d.exprs {
		item := &d.exprs[idx]

		switch {
		case !pathHasPrefix(item.path, prefix):
		case item.kind == unstable.Table || item.kind == unstable.ArrayTable:
			last := idx

			for next := idx + 1; next < len(d.exprs); next++ {
				kind := d.exprs[next].kind
				if kind == unstable.Table || kind == unstable.ArrayTable {
					break
				}

				if kind == unstable.KeyValue {
					last = next
				}
			}

			spans = append(spans, span{
				start: d.blankLinesAbove(d.lineStart(item.start)),
				end:   d.lineEnd(d.exprs[last].end),
			})
		case item.kind == unstable.KeyValue && !pathHasPrefix(item.table, prefix):
			spans = append(spans, span{
				start: d.lineStart(item.start),
				end:   d.lineEnd(item.end),
			})
		}
	}

	slices.SortFunc(spans, func(a, b span) int { return a.start - b.start })

	merged := spans[:0]
	for _, next := range spans {
		if len(merged) > 0 && next.start <= merged[len(merged)-1].end {
			merged[len(merged)-1].end = max(merged[len(merged)-1].end, next.end)

			continue
		}

		merged = append(merged, next)
	}

	return merged
}

// blankLinesAbove returns the start of the run of blank lines that ends at
// the line start pos, pos itself when the line above is not blank.
func (d *document) blankLinesAbove(pos int) int {
	for pos > 0 {
		above := d.lineStart(pos - 1)
		if len(bytes.TrimSpace(d.src[above:pos])) != 0 {
			break
		}

		pos = above
	}

	return pos
}
