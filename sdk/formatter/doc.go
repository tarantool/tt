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
//
// [DecodeYAML] reads YAML the way the Lua and table formats read a console
// answer: as YAML 1.1, with the booleans and timestamps of it, as
// gopkg.in/yaml.v2 read it. [EncodeYAML] writes a value back as tt's
// consoles print it, with the indent and the line folding of gopkg.in/yaml.v2.
package formatter
