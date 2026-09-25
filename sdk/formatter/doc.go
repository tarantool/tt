// Package formatter renders the results of a Tarantool console request.
//
// A Tarantool console answers with a YAML document; [MakeOutput] turns that
// document into the text shown to the user in one of the [Format]s: the YAML
// itself, a Lua expression, or tables (row per record, or transposed). The
// table formats take their look from [Opts]: pseudographics on or off, a
// maximum column width and a [TableDialect] (terminal, Markdown or Jira).
//
// The output is what tt prints in its consoles, byte for byte, so a module
// that renders console results through this package prints them the same
// way tt's own commands do.
package formatter
