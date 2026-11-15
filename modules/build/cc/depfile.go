package cc

import (
	"errors"
	"slices"
	"strings"
)

// errNoTargets reports a line of a depfile with prerequisites and no target:
// what gcc and clang never write, so the file is not one of theirs.
var errNoTargets = errors.New("a rule without targets")

// parseDepfile returns the prerequisites of the rules of data, a depfile as
// gcc -MD and clang -MD write it - make rules of the object on the files the
// compile read, `obj.o: a.c a.h \` - each once, in the order they first
// come. The targets are dropped: -MP adds a rule of no prerequisites for each
// header, `a.h:`, and a rule may name several targets.
//
// The syntax is make's, as the compilers escape it: a backslash before a
// newline continues the line, `\ ` is a space of a path, `\#` a #, and `$$`
// a $; another backslash is a character of a path, as gcc writes a
// backslash of a name. A colon ends the targets when a blank or the end of
// the line follows it, so that a:b.h is a name. A carriage return is a
// blank, for a file of CRLF lines. A line of words and no colon, or a colon
// with no target, is an error.
func parseDepfile(data string) ([]string, error) {
	parser := depParser{rest: data, word: strings.Builder{}, inWord: false,
		inPrereqs: false, words: 0, prereqs: nil}

	for parser.rest != "" {
		err := parser.next()
		if err != nil {
			return nil, err
		}
	}

	err := parser.endLine()
	if err != nil {
		return nil, err
	}

	prereqs := make([]string, 0, len(parser.prereqs))

	for _, prereq := range parser.prereqs {
		if !slices.Contains(prereqs, prereq) {
			prereqs = append(prereqs, prereq)
		}
	}

	return prereqs, nil
}

// depParser is the state of parseDepfile.
type depParser struct {
	// rest is what is left of the depfile.
	rest   string
	word   strings.Builder
	inWord bool
	// inPrereqs is set past the colon of the line.
	inPrereqs bool
	// words are the words of the line before its colon, its targets.
	words   int
	prereqs []string
}

// next takes the next character of rest, or the escape it starts.
func (p *depParser) next() error {
	char := p.rest[0]

	p.rest = p.rest[1:]

	switch {
	case char == '\\' && p.rest != "":
		p.escaped()
	case char == '$' && strings.HasPrefix(p.rest, "$"):
		p.rest = p.rest[1:]

		p.add('$')
	case char == ':' && !p.inPrereqs && (p.rest == "" || isBlankOrEOL(p.rest[0])):
		p.endWord()

		if p.words == 0 {
			return errNoTargets
		}

		p.inPrereqs = true
	case char == '\n':
		return p.endLine()
	case char == ' ' || char == '\t' || char == '\r':
		p.endWord()
	default:
		p.add(char)
	}

	return nil
}

// escaped takes what a backslash, just taken, escapes: a newline, possibly
// of CRLF, a space or a #; any other character leaves the backslash in the
// word.
func (p *depParser) escaped() {
	switch {
	case strings.HasPrefix(p.rest, "\n"):
		p.rest = p.rest[1:]

		p.endWord()
	case strings.HasPrefix(p.rest, "\r\n"):
		p.rest = p.rest[2:]

		p.endWord()
	case p.rest[0] == ' ' || p.rest[0] == '#':
		p.add(p.rest[0])

		p.rest = p.rest[1:]
	default:
		p.add('\\')
	}
}

// add adds char to the word being read.
func (p *depParser) add(char byte) {
	p.word.WriteByte(char)

	p.inWord = true
}

// endWord ends the word being read, if there is one: a prerequisite past
// the colon, a target before it.
func (p *depParser) endWord() {
	if !p.inWord {
		return
	}

	if p.inPrereqs {
		p.prereqs = append(p.prereqs, p.word.String())
	} else {
		p.words++
	}

	p.word.Reset()

	p.inWord = false
}

// endLine ends the logical line being read: one of words has to have had a
// colon.
func (p *depParser) endLine() error {
	p.endWord()

	if p.words > 0 && !p.inPrereqs {
		return errNoColon
	}

	p.inPrereqs = false
	p.words = 0

	return nil
}

// errNoColon reports a line of a depfile with words and no colon.
var errNoColon = errors.New("a line without a colon")

// isBlankOrEOL reports whether char is a blank or ends a line.
func isBlankOrEOL(char byte) bool {
	return char == ' ' || char == '\t' || char == '\r' || char == '\n'
}
