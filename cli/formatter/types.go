package formatter

// renderNode represents content for rendering.
type renderNode interface{}

const (
	scalarRenderNodeType = iota
	arrayRenderNodeType
	mapRenderNodeType
)

// Opts contains formatting options.
type Opts struct {
	TransposeMode bool
	NoGraphics    bool
	ColWidthMax   int
	TableDialect  TableDialect
}

const (
	defaultFormatStr     = ""
	yamlFormatStr        = "yaml"
	yamlFormatShortStr   = "y"
	luaFormatStr         = "lua"
	luaFormatShortStr    = "l"
	tableFormatStr       = "table"
	tableFormatShortStr  = "t"
	ttableFormatStr      = "ttable"
	ttableFormatShortStr = "T"
)

// Format defines a set of supported output format.
type Format int

const (
	DefaultFormat Format = iota
	YamlFormat
	LuaFormat
	TableFormat
	TTableFormat
)

// ParseFormat parses a output format string representation. It supports
// mixed case letters.
func ParseFormat(str string) (Format, bool) {
	switch str {
	case defaultFormatStr:
		return DefaultFormat, true
	case yamlFormatStr, yamlFormatShortStr:
		return YamlFormat, true
	case luaFormatStr, luaFormatShortStr:
		return LuaFormat, true
	case tableFormatStr, tableFormatShortStr:
		return TableFormat, true
	case ttableFormatStr, ttableFormatShortStr:
		return TTableFormat, true
	}
	return DefaultFormat, false
}

// String returns a string representation of the output format.
func (l Format) String() string {
	switch l {
	case DefaultFormat:
		return defaultFormatStr
	case YamlFormat:
		return yamlFormatStr
	case LuaFormat:
		return luaFormatStr
	case TableFormat:
		return tableFormatStr
	case TTableFormat:
		return ttableFormatStr
	default:
		panic("Unknown output format")
	}
}

// TableDialect defines a set of supported table dialect.
type TableDialect int

const (
	DefaultTableDialect TableDialect = iota
	MarkdownTableDialect
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
	switch str {
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
func (f TableDialect) String() string {
	switch f {
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
