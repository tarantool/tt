package manifest

import (
	"bytes"
	"errors"
	"fmt"
)

// This file finds where the value of a key/value is in the source. The
// parser reports the extent of a string and of no other value: an array
// has no range, an inline table the range of its opening brace. So the
// value is scanned, from the '=' after its key, as TOML writes one: quoted
// strings of the four kinds, arrays and inline tables nested in each other
// across lines, with the comments of an array in them, and a scalar up to
// the end of its line or a comment.

// valueSpan returns the extent of the value of the key/value item of d.
func (d *document) valueSpan(item *tomlExpr) (span, error) {
	pos := item.keyEnd

	for pos < len(d.src) && d.src[pos] != '=' {
		pos++
	}

	if pos == len(d.src) {
		return span{}, fmt.Errorf("%w: no '=' after the key %s", ErrUnsupportedForm,
			joinKeys(item.path))
	}

	pos = skipSpaces(d.src, pos+1)

	end, err := valueEnd(d.src, pos)
	if err != nil {
		return span{}, fmt.Errorf("%w: the value of %s: %w", ErrUnsupportedForm,
			joinKeys(item.path), err)
	}

	return span{start: pos, end: end}, nil
}

// errUnterminated reports a value the scan of valueEnd runs off the source
// in, which the parser would have refused.
var errUnterminated = errors.New("unterminated")

// tripleQuote is the length of the quotes of a multi-line string, and
// maxQuotesInside the most quotes of it that may come right before them.
const (
	tripleQuote     = 3
	maxQuotesInside = 2
)

// valueEnd returns the offset just past the value that starts at pos.
func valueEnd(src []byte, pos int) (int, error) {
	if pos >= len(src) {
		return 0, errUnterminated
	}

	switch src[pos] {
	case '"', '\'':
		return stringEnd(src, pos)
	case '[', '{':
		return bracketEnd(src, pos)
	}

	// A scalar: a number, a boolean, a date-time, which may hold a space,
	// up to a comment or the end of the line, spaces trimmed.
	end := pos
	for end < len(src) && src[end] != '\n' && src[end] != '\r' && src[end] != '#' &&
		src[end] != ',' && src[end] != ']' && src[end] != '}' {
		end++
	}

	for end > pos && (src[end-1] == ' ' || src[end-1] == '\t') {
		end--
	}

	return end, nil
}

// bracketEnd returns the offset just past the bracket that closes the array
// or the inline table opened at pos, over the strings and comments in it.
func bracketEnd(src []byte, pos int) (int, error) {
	depth := 0

	for pos < len(src) {
		switch src[pos] {
		case '"', '\'':
			end, err := stringEnd(src, pos)
			if err != nil {
				return 0, err
			}

			pos = end

			continue
		case '#':
			next := bytes.IndexByte(src[pos:], '\n')
			if next < 0 {
				return 0, errUnterminated
			}

			pos += next
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return pos + 1, nil
			}
		}

		pos++
	}

	return 0, errUnterminated
}

// stringEnd returns the offset just past the string that starts at pos: a
// basic or a literal one, on one line or several.
func stringEnd(src []byte, pos int) (int, error) {
	quote := src[pos]
	triple := bytes.HasPrefix(src[pos:], []byte{quote, quote, quote})

	if triple {
		pos += tripleQuote
	} else {
		pos++
	}

	for pos < len(src) {
		switch {
		case src[pos] == '\\' && quote == '"':
			pos += 2

			continue
		case src[pos] != quote:
			pos++

			continue
		case !triple:
			return pos + 1, nil
		case bytes.HasPrefix(src[pos:], []byte{quote, quote, quote}):
			// Up to two quotes of the string may come right before the three
			// that close it: """a"""" is `a"`.
			end := pos + tripleQuote
			for end < len(src) && end < pos+tripleQuote+maxQuotesInside && src[end] == quote {
				end++
			}

			return end, nil
		default:
			pos++
		}
	}

	return 0, errUnterminated
}

// skipSpaces returns the offset of the first byte from pos that is neither
// a space nor a tab.
func skipSpaces(src []byte, pos int) int {
	for pos < len(src) && (src[pos] == ' ' || src[pos] == '\t') {
		pos++
	}

	return pos
}
