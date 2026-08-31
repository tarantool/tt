package manifest

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// This file classifies a dependency declaration into one of the forms the
// editor knows how to rewrite, and turns it into the byte ranges edit.go
// splices. Anything that does not classify cleanly is refused with
// ErrUnsupportedForm rather than guessed at.

// declForm is the shape a dependency is written in.
type declForm int

const (
	// formShort is `name = ">=1.0.0"` - a bare constraint string.
	formShort declForm = iota
	// formInline is `name = { source = "registry", version = ">=1.0.0" }`.
	formInline
	// formTable covers every spelling of the long form that puts the fields on
	// their own lines: a [dependencies.name] header with its body, and the
	// dotted keys (`dependencies.name.version = "..."`) that mean the same
	// thing. They are one form because they index identically - both produce
	// key/value expressions with the path dependencies.name.<field>.
	formTable
)

// declaration is one dependency located in a document.
type declaration struct {
	form declForm
	// matches indexes every expression that belongs to the declaration, in
	// source order.
	matches []int
	// header indexes the [dependencies.name] table header, or -1 when the
	// declaration has none.
	header int
	// hasVersion reports whether a string-valued version constraint was found;
	// version is its token range and versionData its decoded text.
	hasVersion  bool
	version     span
	versionData string
}

// find locates name in table, classifying the declaration form.
//
// It returns ErrNoSuchDependency when nothing declares the name, and an error
// wrapping ErrUnsupportedForm when something does but in a shape that cannot
// be rewritten safely.
func (d *document) find(table DepTable, name string) (*declaration, error) {
	matches, header, err := d.collect(table, name)
	if err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("%s.%s: %w", table, name, ErrNoSuchDependency)
	}

	entries, fields := d.split(matches)

	switch {
	case len(entries) > 1:
		return nil, refuse(table, name, "it is declared more than once")
	case len(entries) == 1 && (header >= 0 || len(fields) > 0):
		return nil, refuse(table, name,
			"it is declared both as a single key and as a table")
	case len(entries) == 1:
		return d.describeEntry(table, name, matches, entries[0])
	}

	return d.describeTable(table, name, matches, header, fields)
}

// collect gathers the expressions belonging to a dependency and rejects the
// table shapes the editor cannot follow: an array of tables, a table nested
// below the dependency (which a removal would strand), and the same
// dependency opened by two headers.
func (d *document) collect(table DepTable, name string) ([]int, int, error) {
	var matches []int

	header := -1

	for idx := range d.exprs {
		item := &d.exprs[idx]
		if !hasDepPrefix(item.path, table, name) {
			continue
		}

		switch item.kind {
		case unstable.Table:
			if len(item.path) != depthEntry {
				return nil, 0, refuse(table, name,
					"it has a nested table [%s]", joinKeys(item.path))
			}

			if header >= 0 {
				return nil, 0, refuse(table, name, "its table header appears twice")
			}

			header = idx
		case unstable.ArrayTable:
			return nil, 0, refuse(table, name, "it is declared as an array of tables")
		case unstable.KeyValue:
		default:
			return nil, 0, refuse(table, name, "it contains a %s expression", item.kind)
		}

		matches = append(matches, idx)
	}

	err := d.checkContiguous(table, name, matches, header)
	if err != nil {
		return nil, 0, err
	}

	return matches, header, nil
}

// checkContiguous refuses a declaration whose header is separated from its own
// keys by another table header. Removal deletes one contiguous region from the
// header to the last key, so a foreign header inside that region would be
// deleted with it.
func (d *document) checkContiguous(table DepTable, name string, matches []int, header int) error {
	if header < 0 || len(matches) == 0 {
		return nil
	}

	last := matches[len(matches)-1]
	for i := header + 1; i < last; i++ {
		if d.exprs[i].kind == unstable.Table || d.exprs[i].kind == unstable.ArrayTable {
			return refuse(table, name, "another table is declared inside its body")
		}
	}

	return nil
}

// split partitions the key/value expressions of a declaration into the ones
// that are the declaration itself and the ones that are its long-form fields.
func (d *document) split(matches []int) ([]int, []int) {
	var entries, fields []int

	for _, idx := range matches {
		item := &d.exprs[idx]
		if item.kind != unstable.KeyValue {
			continue
		}

		if len(item.path) == depthEntry {
			entries = append(entries, idx)
		} else {
			fields = append(fields, idx)
		}
	}

	return entries, fields
}

// describeEntry classifies a dependency written as one key: a bare constraint
// string or an inline table.
func (d *document) describeEntry(
	table DepTable, name string, matches []int, entry int,
) (*declaration, error) {
	item := &d.exprs[entry]

	decl := &declaration{
		form:        formShort,
		matches:     matches,
		header:      -1,
		hasVersion:  false,
		version:     span{start: 0, end: 0},
		versionData: "",
	}

	switch item.valueKind {
	case unstable.String:
		decl.hasVersion = true
		decl.version = item.value
		decl.versionData = item.valueData
	case unstable.InlineTable:
		decl.form = formInline
		decl.hasVersion = item.hasVersion
		decl.version = item.version
		decl.versionData = item.versionData
	default:
		return nil, refuse(table, name, "its value is a %s, not a constraint or a table",
			item.valueKind)
	}

	return decl, nil
}

// describeTable classifies a dependency written as a [dependencies.name] table
// or as dotted keys, and finds its version field.
func (d *document) describeTable(
	table DepTable, name string, matches []int, header int, fields []int,
) (*declaration, error) {
	decl := &declaration{
		form:        formTable,
		matches:     matches,
		header:      header,
		hasVersion:  false,
		version:     span{start: 0, end: 0},
		versionData: "",
	}

	for _, idx := range fields {
		item := &d.exprs[idx]
		if len(item.path) != depthField {
			return nil, refuse(table, name, "it has a nested key %s", joinKeys(item.path))
		}

		if item.path[depthField-1] != versionKey || item.valueKind != unstable.String {
			continue
		}

		decl.hasVersion = true
		decl.version = item.value
		decl.versionData = item.valueData
	}

	return decl, nil
}

// removalSpans returns the byte ranges RemoveDependency deletes, in ascending
// order and never overlapping.
//
// A declaration with a table header is deleted as one region running from the
// header's line to the line of its last key, so comments and blank lines
// written inside the block go with it. Without a header - dotted keys, or a
// single-key declaration - each line goes on its own, and whatever sits
// between them stays.
func (d *document) removalSpans(decl *declaration) []span {
	if decl.header >= 0 {
		last := decl.matches[len(decl.matches)-1]

		return []span{{
			start: d.lineStart(d.exprs[decl.header].start),
			end:   d.lineEnd(d.exprs[last].end),
		}}
	}

	spans := make([]span, 0, len(decl.matches))
	for _, idx := range decl.matches {
		spans = append(spans, span{
			start: d.lineStart(d.exprs[idx].start),
			end:   d.lineEnd(d.exprs[idx].end),
		})
	}

	return spans
}

// refuse builds an ErrUnsupportedForm error naming the dependency and the
// construct that stopped the edit, so the user knows what to fix by hand.
func refuse(table DepTable, name, reason string, args ...any) error {
	return fmt.Errorf("%s.%s: %w: %s", table, name, ErrUnsupportedForm,
		fmt.Sprintf(reason, args...))
}

// joinKeys renders a key path the way it appears in the file.
func joinKeys(path []string) string {
	quoted := make([]string, 0, len(path))
	for _, key := range path {
		quoted = append(quoted, quoteTOMLKey(key))
	}

	return strings.Join(quoted, ".")
}
