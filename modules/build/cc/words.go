package cc

import (
	"errors"
	"strings"
)

// errUnterminatedQuote and errTrailingBackslash report a value of CC or CXX
// that splitWords cannot split.
var (
	errUnterminatedQuote = errors.New("unterminated quote")
	errTrailingBackslash = errors.New("backslash at the end")
)

// splitWords splits value into words as a POSIX shell splits a simple
// command, with quoting and none of the expansions: CC="ccache cc" is the
// program ccache with the argument cc, CC="zig cc" is zig cc, and a compiler
// in a directory with a space is CC="'/opt/my tools/cc'". Words are
// separated by blanks (space, tab, newline, carriage return). Inside '...'
// every character is literal; inside "..." a backslash escapes only $, `, ",
// \ and a newline, which it removes, and is literal before any other
// character; outside quotes a backslash makes the next character literal,
// and removes a newline. $, ` and globs are kept as they are: the value is
// not run by a shell. A quoted empty string, of two single or two double
// quotes, is an empty word. A quote left open, or a backslash ending value,
// is an error.
func splitWords(value string) ([]string, error) {
	split := splitter{rest: value, words: nil, word: strings.Builder{}, inWord: false}

	for split.rest != "" {
		err := split.next()
		if err != nil {
			return nil, err
		}
	}

	split.end()

	return split.words, nil
}

// splitter is the state of splitWords.
type splitter struct {
	// rest is what is left of the value to split.
	rest  string
	words []string
	word  strings.Builder
	// inWord is set from the first character of a word, a quote that makes
	// an empty word included.
	inWord bool
}

// next takes the next character of rest, or the quoted string it opens.
func (s *splitter) next() error {
	switch char := s.take(); char {
	case ' ', '\t', '\n', '\r':
		s.end()
	case '\\':
		if s.rest == "" {
			return errTrailingBackslash
		}

		s.inWord = true

		if s.rest[0] != '\n' {
			s.word.WriteByte(s.rest[0])
		}

		s.rest = s.rest[1:]
	case '\'':
		quoted, rest, closed := strings.Cut(s.rest, "'")
		if !closed {
			return errUnterminatedQuote
		}

		s.inWord = true
		s.rest = rest

		s.word.WriteString(quoted)
	case '"':
		s.inWord = true

		return s.doubleQuoted()
	default:
		s.inWord = true

		s.word.WriteByte(char)
	}

	return nil
}

// doubleQuoted takes what "..." holds from rest, which starts past the
// opening quote, up to the closing one.
func (s *splitter) doubleQuoted() error {
	for s.rest != "" {
		switch char := s.take(); {
		case char == '"':
			return nil
		case char == '\\' && s.rest != "" && strings.IndexByte("$`\"\\\n", s.rest[0]) >= 0:
			if s.rest[0] != '\n' {
				s.word.WriteByte(s.rest[0])
			}

			s.rest = s.rest[1:]
		default:
			s.word.WriteByte(char)
		}
	}

	return errUnterminatedQuote
}

// take returns the first character of rest, which is not empty, and drops it
// from rest.
func (s *splitter) take() byte {
	char := s.rest[0]

	s.rest = s.rest[1:]

	return char
}

// end ends the word being split, if there is one.
func (s *splitter) end() {
	if s.inWord {
		s.words = append(s.words, s.word.String())
		s.word.Reset()

		s.inWord = false
	}
}
