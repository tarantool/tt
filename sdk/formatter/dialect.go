package formatter

import (
	"strings"
)

// TableDialect defines a set of supported table dialect.
type TableDialect int

const (
	// DefaultTableDialect draws tables for a terminal, with pseudographics
	// or with spaces as [Opts.Graphics] says.
	DefaultTableDialect TableDialect = iota
	// MarkdownTableDialect writes tables to paste into Markdown.
	MarkdownTableDialect
	// JiraTableDialect writes tables to paste into Jira.
	JiraTableDialect
)

const (
	defaultTableDialectStr  = "default"
	markdownTableDialectStr = "markdown"
	jiraTableDialectStr     = "jira"
)

// ParseTableDialect parses a table dialect string representation. It supports
// mixed case letters.
func ParseTableDialect(str string) (TableDialect, bool) {
	switch strings.ToLower(str) {
	case defaultTableDialectStr:
		return DefaultTableDialect, true
	case markdownTableDialectStr:
		return MarkdownTableDialect, true
	case jiraTableDialectStr:
		return JiraTableDialect, true
	}

	return DefaultTableDialect, false
}

// String returns a string representation of the table dialect.
func (d TableDialect) String() string {
	switch d {
	case DefaultTableDialect:
		return defaultTableDialectStr
	case MarkdownTableDialect:
		return markdownTableDialectStr
	case JiraTableDialect:
		return jiraTableDialectStr
	default:
		panic("Unknown table dialect")
	}
}
