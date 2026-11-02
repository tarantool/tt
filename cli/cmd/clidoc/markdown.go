package clidoc

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// preWidth is the column a preformatted line is wrapped at, the one the help
// wraps its output at.
const preWidth = 80

// blockKind tells what a block of a help text is.
type blockKind int

const (
	// paragraph is prose: lines at the margin.
	paragraph blockKind = iota
	// list is a list: lines at the margin starting with "* " or "- ", each
	// with the indented lines after it.
	list
	// preformatted is indented lines, kept as they are: a table of values,
	// a format, a sample of a file.
	preformatted
)

// block is a run of lines of one kind.
type block struct {
	kind  blockKind
	lines []string
}

var (
	// escaper escapes the characters Markdown would take for markup or HTML.
	escaper = strings.NewReplacer(
		`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`,
		"&", "&amp;", "<", "&lt;", ">", "&gt;",
	)
	// markupStart matches the start of a line Markdown would take for a
	// heading or a list.
	markupStart = regexp.MustCompile(`^(#|\+|-|\d+\.)`)
)

// Markdown renders a help text as Markdown. A help text is plain text
// wrapped for a terminal: paragraphs at the margin, lists of "* " or "- "
// items, and indented lines that keep their layout. The paragraphs and the
// lists become Markdown ones, with every character Markdown would take for
// markup escaped, and the indented lines a code block.
func Markdown(text string) string {
	blocks := parse(text)

	var out strings.Builder

	for _, block := range blocks {
		switch block.kind {
		case paragraph:
			for _, line := range block.lines {
				out.WriteString(escapeLine(line) + "\n")
			}

			out.WriteString("\n")
		case list:
			for _, item := range block.lines {
				out.WriteString("- " + escape(item) + "\n")
			}

			out.WriteString("\n")
		case preformatted:
			lines := make([]string, 0, len(block.lines))

			for _, line := range dedent(block.lines) {
				lines = append(lines, wrapLine(line, preWidth)...)
			}

			writeBlock(&out, "text", lines)
		}
	}

	return out.String()
}

// parse splits a help text into blocks. A blank line ends a block, and so
// does a line of another kind: help texts start a list or an indented table
// right after the line that introduces it. Indented lines go on after a
// blank line: a sample of a file has blank lines of its own.
func parse(text string) []block {
	var (
		blocks []block
		// current points into blocks. It stays valid until the next append,
		// which only start does, and start returns the new pointer.
		current *block
		// blank is the preformatted block a blank line was met in.
		blank *block
	)

	start := func(kind blockKind) *block {
		blocks = append(blocks, block{kind: kind, lines: nil})

		return &blocks[len(blocks)-1]
	}

	for line := range strings.SplitSeq(strings.Trim(text, "\n"), "\n") {
		line = strings.TrimRight(line, " \t")

		indented := line != strings.TrimLeft(line, " \t")

		if blank != nil && indented {
			current = blank
			current.lines = append(current.lines, "")
		}

		blank = nil

		switch {
		case line == "":
			if current != nil && current.kind == preformatted {
				blank = current
			}

			current = nil
		case isListItem(line):
			if current == nil || current.kind != list {
				current = start(list)
			}

			current.lines = append(current.lines, strings.TrimSpace(line[2:]))
		case indented && current != nil && current.kind == list:
			last := len(current.lines) - 1

			current.lines[last] += " " + strings.TrimSpace(line)
		case indented:
			if current == nil || current.kind != preformatted {
				current = start(preformatted)
			}

			current.lines = append(current.lines, line)
		default:
			if current == nil || current.kind != paragraph {
				current = start(paragraph)
			}

			current.lines = append(current.lines, line)
		}
	}

	return blocks
}

// isListItem tells whether line starts an item of a list.
func isListItem(line string) bool {
	return strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "- ")
}

// escape escapes the characters of text Markdown would take for markup.
func escape(text string) string {
	return escaper.Replace(text)
}

// escapeLine escapes a line of a paragraph, its start included.
func escapeLine(line string) string {
	line = escape(line)

	if loc := markupStart.FindStringIndex(line); loc != nil {
		// A heading or a list marker is escaped at its last character: "#"
		// and "+" themselves, the dot of "1.".
		at := loc[1] - 1

		line = line[:at] + `\` + line[at:]
	}

	return line
}

// wrapLine breaks line at spaces into lines no wider than width, as the help
// wraps its output (cli/cmd.wrapHelp): a line with a gap of two or more
// spaces after its first word continues under the text after the gap, any
// other line at its own indentation. A word wider than width is kept whole.
func wrapLine(line string, width int) []string {
	if utf8.RuneCountInString(line) <= width {
		return []string{line}
	}

	body := strings.TrimLeft(line, " ")
	prefix := line[:len(line)-len(body)]
	indent := prefix

	if gap := strings.Index(body, "  "); gap > 0 {
		text := strings.TrimLeft(body[gap:], " ")
		head := line[:len(line)-len(text)]

		if text != "" && utf8.RuneCountInString(head) <= width/2 {
			prefix, indent, body = head, strings.Repeat(" ", utf8.RuneCountInString(head)), text
		}
	}

	var (
		lines   []string
		current = prefix
		empty   = true
	)

	for word := range strings.FieldsSeq(body) {
		if !empty && utf8.RuneCountInString(current)+1+utf8.RuneCountInString(word) > width {
			lines = append(lines, current)
			current, empty = indent, true
		}

		if !empty {
			current += " "
		}

		current += word

		empty = false
	}

	return append(lines, current)
}
