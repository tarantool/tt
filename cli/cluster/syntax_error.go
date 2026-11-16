package cluster

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"
	"github.com/tarantool/go-config/v2/collectors"
)

// fileSourceKey is the key go-config names the cluster config file by in a
// collectors.FormatParseError.
const fileSourceKey = "file"

// yamlLine matches what the YAML parser of go-config, yaml.v3, says of a
// document that does not parse: the line and the problem there.
var yamlLine = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// SyntaxError is the error for a cluster config file that is no YAML: where
// in the file, and what the parser found there, as an editor and a compiler
// tell it, <path>:<line>:<column>: <problem>.
type SyntaxError struct {
	// Path is the file.
	Path string
	// Line and Column are where the problem is, 1-based, zero when not
	// known.
	Line, Column int
	// Problem is what the parser says of it.
	Problem string
	// Err is the error of the parser.
	Err error
}

// Error tells where in the file the problem is, and what it is.
func (e *SyntaxError) Error() string {
	where := e.Path
	if e.Line > 0 {
		where += ":" + strconv.Itoa(e.Line)

		if e.Column > 0 {
			where += ":" + strconv.Itoa(e.Column)
		}
	}

	return where + ": " + e.Problem
}

// Unwrap returns the error of the parser.
func (e *SyntaxError) Unwrap() error {
	return e.Err
}

// loadError returns the error for err, the failure of go-config to load the
// cluster config file path: a SyntaxError for a file that is no YAML, which
// the error of go-config names by an internal key and the words of its
// layers, and the error as it is otherwise.
func loadError(path string, err error) error {
	syntax := syntaxError(path, err)
	if syntax != nil {
		return fmt.Errorf("unable to load config: %w", syntax)
	}

	return fmt.Errorf("unable to load config from %q: %w", path, err)
}

// syntaxError returns err as a SyntaxError of the file path when it is an
// error of go-config parsing the file, nil otherwise.
//
// The problem is what yaml.v3 says, which is a port of the libyaml Tarantool
// parses the file with, so Tarantool tells the same of it. yaml.v3 tells no
// column, and the line it tells of a problem of the parser is the one before
// it, as it counts the lines of its context from zero there: the position is
// where goccy/go-yaml finds the problem, the line of yaml.v3 when it finds
// none.
func syntaxError(path string, err error) *SyntaxError {
	var parseErr *collectors.FormatParseError

	if !errors.As(err, &parseErr) || parseErr.Key != fileSourceKey || parseErr.Err == nil {
		return nil
	}

	problem := strings.TrimPrefix(parseErr.Err.Error(), collectors.ErrUnmarshall.Error()+": ")
	syntax := &SyntaxError{Path: path, Line: 0, Column: 0, Problem: problem, Err: parseErr.Err}

	match := yamlLine.FindStringSubmatch(problem)
	if match == nil {
		syntax.Problem = strings.TrimPrefix(problem, "yaml: ")

		return syntax
	}

	syntax.Line, _ = strconv.Atoi(match[1])
	syntax.Problem = match[2]

	line, column := problemPosition(path)
	if line > 0 {
		syntax.Line, syntax.Column = line, column
	}

	return syntax
}

// tokenError is an error of goccy/go-yaml that tells the token it is about.
type tokenError interface {
	GetToken() *token.Token
}

// problemPosition returns where goccy/go-yaml finds the YAML file path not
// to parse, the line and the column, zeros when it parses it, or cannot
// read it, or tells no position.
func problemPosition(path string) (int, int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0
	}

	_, err = parser.ParseBytes(data, 0)

	var tokenErr tokenError

	if err == nil || !errors.As(err, &tokenErr) {
		return 0, 0
	}

	tok := tokenErr.GetToken()
	if tok == nil || tok.Position == nil {
		return 0, 0
	}

	return tok.Position.Line, tok.Position.Column
}
