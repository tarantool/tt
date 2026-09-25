package output

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/pflag"
)

// FormatFlag is a -o/--format flag declared by [BindFormat]: the format the
// user named, checked against the formats the command accepts, or the
// command's default.
type FormatFlag struct {
	value    Format
	fallback Format
	formats  []Format
}

// BindFormat declares -o/--format on flags, accepting formats and defaulting to
// fallback, and returns the flag to read the chosen format from once the
// flags are parsed. Every command that prints a result declares its format
// flag with BindFormat, so the flag has one name, one shorthand and one help
// text everywhere: the accepted values in the order given, and the default.
//
// A value outside formats fails flag parsing with ErrUnknownFormat. An
// empty value selects fallback, as [ResolveFormat] does. The terminal plays
// no part: without the flag the format is fallback whatever stdout is.
//
// BindFormat panics when formats is empty, names a format twice or does not
// include fallback - a mistake in the command's code, not in its input - and,
// as pflag does, when flags already has a format or o flag.
func BindFormat(flags *pflag.FlagSet, fallback Format, formats ...Format) *FormatFlag {
	if len(formats) == 0 {
		panic("output.BindFormat: no formats")
	}

	for i, format := range formats {
		if slices.Contains(formats[:i], format) {
			panic(fmt.Sprintf("output.BindFormat: format %q given twice", format))
		}
	}

	if !slices.Contains(formats, fallback) {
		panic(fmt.Sprintf("output.BindFormat: default %q is not among %s",
			fallback, spellFormats(formats)))
	}

	flag := &FormatFlag{value: "", fallback: fallback, formats: slices.Clone(formats)}
	flags.VarP(flag, "format", "o", "output format: "+spellFormats(formats))

	return flag
}

// Format returns the format the flag selects: the value given, or the
// default when the flag was not given or given empty.
func (f *FormatFlag) Format() Format {
	return ResolveFormat(string(f.value), f.fallback)
}

// String returns the selected format; pflag shows it as the default in the
// help text.
func (f *FormatFlag) String() string {
	return string(f.Format())
}

// Set takes a value from the command line. A format the command does not
// accept is ErrUnknownFormat.
func (f *FormatFlag) Set(value string) error {
	if value != "" && !slices.Contains(f.formats, Format(value)) {
		return fmt.Errorf("%w %q (want %s)", ErrUnknownFormat, value, spellFormats(f.formats))
	}

	f.value = Format(value)

	return nil
}

// Type names the flag's value type in the help text.
func (*FormatFlag) Type() string {
	return "string"
}

// spellFormats lists formats for people: "table", "table or json",
// "table, json or yaml".
func spellFormats(formats []Format) string {
	names := make([]string, len(formats))
	for i, format := range formats {
		names[i] = string(format)
	}

	if len(names) == 1 {
		return names[0]
	}

	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}
