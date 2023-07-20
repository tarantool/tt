package formatter

import (
	"strings"
)

const (
	yamlFormatStr   = "yaml"
	luaFormatStr    = "lua"
	tableFormatStr  = "table"
	ttableFormatStr = "ttable"
)

// Format defines a set of supported output format.
type Format int

const (
	YamlFormat Format = iota
	LuaFormat
	TableFormat
	TTableFormat
	FormatsAmount
)

const (
	// DefaultFormat is a default format.
	DefaultFormat Format = YamlFormat
)

// ParseFormat parses a output format string representation. It supports
// mixed case letters.
func ParseFormat(str string) (Format, bool) {
	switch strings.ToLower(str) {
	case yamlFormatStr:
		return YamlFormat, true
	case luaFormatStr:
		return LuaFormat, true
	case tableFormatStr:
		return TableFormat, true
	case ttableFormatStr:
		return TTableFormat, true
	}
	return DefaultFormat, false
}

// String returns a string representation of the output format.
func (f Format) String() string {
	switch f {
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

// MakeOutput returns formatted output from a YAML data depending on
// the specified output format and passed formatting options.
func MakeOutput(format Format, data string, opts Opts) (string, error) {
	switch format {
	case YamlFormat:
		return data + "\n", nil
	case LuaFormat:
		return makeLuaOutput(data)
	case TableFormat:
		return makeTableOutput(data, false, opts)
	case TTableFormat:
		return makeTableOutput(data, true, opts)
	default:
		panic("Unknown render case")
	}
}
