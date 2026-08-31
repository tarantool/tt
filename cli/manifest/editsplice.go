package manifest

import (
	"bytes"

	"github.com/pelletier/go-toml/v2/unstable"
)

// This file does the actual text surgery: it turns a located span into new
// source bytes. Every function here returns a fresh slice and leaves the
// document's own source untouched, so a failed edit cannot half-apply.

// replace swaps the bytes of one token for new text.
func (d *document) replace(at span, text string) []byte {
	out := make([]byte, 0, len(d.src)-(at.end-at.start)+len(text))

	out = append(out, d.src[:at.start]...)
	out = append(out, text...)
	out = append(out, d.src[at.end:]...)

	return out
}

// cut deletes the given spans, which must be ascending and non-overlapping.
func (d *document) cut(spans []span) []byte {
	out := make([]byte, 0, len(d.src))
	prev := 0

	for _, at := range spans {
		out = append(out, d.src[prev:at.start]...)
		prev = at.end
	}

	return append(out, d.src[prev:]...)
}

// insert adds a short-form declaration for a dependency the document does not
// have yet, and returns the new source.
//
// Where it lands depends on what the file already has, in this order:
//
//  1. an explicit [dependencies] header - after the last key that header owns,
//     or straight after the header when it owns none. Sub-tables of the
//     section ([dependencies.helper]) are stepped over rather than appended
//     to, since a key after their header would land inside them;
//  2. no header but root-level dotted keys (dependencies.foo = "...") - as one
//     more dotted key after the last of them. TOML forbids opening a
//     [dependencies] table that dotted keys already defined, so a new section
//     would not parse;
//  3. neither - a fresh section at the end of the file.
func (d *document) insert(table DepTable, name, constraint string) []byte {
	entry := formatEntry(name, constraint)

	header := d.sectionHeader(table)
	if header >= 0 {
		anchor := d.lastSectionKey(table, header)

		return d.spliceLine(d.lineEnd(d.exprs[anchor].end), d.indentOf(anchor)+entry)
	}

	dotted := d.lastRootDottedKey(table)
	if dotted >= 0 {
		line := d.indentOf(dotted) + quoteTOMLKey(string(table)) + "." + entry

		return d.spliceLine(d.lineEnd(d.exprs[dotted].end), line)
	}

	return d.appendSection(table, entry)
}

// sectionHeader returns the index of the [dependencies]/[dev_dependencies]
// header, or -1 when the document has none.
func (d *document) sectionHeader(table DepTable) int {
	for i := range d.exprs {
		item := &d.exprs[i]
		if item.kind == unstable.Table && len(item.path) == 1 && item.path[0] == string(table) {
			return i
		}
	}

	return -1
}

// lastSectionKey returns the index of the last key/value written directly
// under the section header, falling back to the header itself for an empty
// section.
func (d *document) lastSectionKey(table DepTable, header int) int {
	anchor := header

	for idx := header + 1; idx < len(d.exprs); idx++ {
		item := &d.exprs[idx]
		if item.kind != unstable.KeyValue {
			continue
		}

		if len(item.table) == 1 && item.table[0] == string(table) {
			anchor = idx
		}
	}

	return anchor
}

// lastRootDottedKey returns the index of the last root-level dotted key that
// declares something in the table, or -1 when there is none.
func (d *document) lastRootDottedKey(table DepTable) int {
	found := -1

	for idx := range d.exprs {
		item := &d.exprs[idx]
		if item.kind != unstable.KeyValue || item.table != nil {
			continue
		}

		if len(item.path) >= depthEntry && item.path[0] == string(table) {
			found = idx
		}
	}

	return found
}

// indentOf returns the leading whitespace of the line an expression starts on,
// so an inserted line lines up with the ones around it.
func (d *document) indentOf(idx int) string {
	start := d.exprs[idx].start

	return string(d.src[d.lineStart(start):start])
}

// spliceLine inserts one line at a line boundary.
//
// The one interesting case is a file whose last line has no line break: there
// the break has to be written before the new line rather than after it, so the
// file keeps ending the way it did.
func (d *document) spliceLine(offset int, line string) []byte {
	out := make([]byte, 0, len(d.src)+len(line)+len(d.newline))

	out = append(out, d.src[:offset]...)

	if offset == len(d.src) && !d.trailingNewline {
		out = append(out, d.newline...)

		return append(out, line...)
	}

	out = append(out, line...)
	out = append(out, d.newline...)

	return append(out, d.src[offset:]...)
}

// appendSection adds a new dependency section at the end of the file,
// separated from what is already there by one blank line.
//
// The file's trailing-newline habit is preserved either way: a file that ended
// without a line break still ends without one afterwards.
func (d *document) appendSection(table DepTable, entry string) []byte {
	out := bytes.Clone(d.src)

	if len(out) > 0 {
		if !d.trailingNewline {
			out = append(out, d.newline...)
		}

		// One blank line, unless the file already ends with one.
		if !bytes.HasSuffix(out, []byte(d.newline+d.newline)) {
			out = append(out, d.newline...)
		}
	}

	out = append(out, '[')
	out = append(out, table...)
	out = append(out, ']')
	out = append(out, d.newline...)
	out = append(out, entry...)

	if d.trailingNewline || len(d.src) == 0 {
		out = append(out, d.newline...)
	}

	return out
}
