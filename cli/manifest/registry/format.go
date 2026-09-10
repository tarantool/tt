package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// yamlIndent is tt's usual YAML indent, and tabColumnPadding the gap between
// table columns.
const (
	yamlIndent       = 2
	tabColumnPadding = 2
)

// Format is how a listing is rendered.
type Format string

const (
	// FormatTable is the human-readable column layout.
	FormatTable Format = "table"
	// FormatJSON is machine-readable JSON.
	FormatJSON Format = "json"
	// FormatYAML is machine-readable YAML.
	FormatYAML Format = "yaml"
)

// ParseFormat resolves the -o value against whether stdout is a terminal.
//
// An explicit value always wins. With none given the default follows the tt
// convention the other package listings use: a terminal gets the table, a pipe
// or a file gets YAML, so output consumed by something else is parseable
// without the caller having to remember a flag.
func ParseFormat(raw string, tty bool) (Format, error) {
	switch Format(raw) {
	case "":
		if tty {
			return FormatTable, nil
		}

		return FormatYAML, nil
	case FormatTable:
		return FormatTable, nil
	case FormatJSON:
		return FormatJSON, nil
	case FormatYAML:
		return FormatYAML, nil
	default:
		return "", stateErrorf("%w %q (want table, json or yaml)", ErrUnknownFormat, raw)
	}
}

// renderJSON writes indented JSON with a trailing newline, so the output is
// pleasant both piped into jq and read directly.
func renderJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")

	err := encoder.Encode(value)
	if err != nil {
		return fmt.Errorf("rendering JSON: %w", err)
	}

	return nil
}

// renderYAML writes YAML at tt's usual two-space indent.
func renderYAML(out io.Writer, value any) error {
	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(yamlIndent)

	err := encoder.Encode(value)
	if err != nil {
		return fmt.Errorf("rendering YAML: %w", err)
	}

	return encoder.Close() //nolint:wrapcheck // Close reports the same encode error.
}

// renderRows writes a padded table: a header line followed by one line per
// row, every line already tab-separated.
func renderRows(out io.Writer, header string, rows []string) error {
	table := tabwriter.NewWriter(out, 0, 0, tabColumnPadding, ' ', 0)

	_, err := fmt.Fprintln(table, header)
	if err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}

	for _, row := range rows {
		_, err = fmt.Fprintln(table, row)
		if err != nil {
			return fmt.Errorf("rendering table: %w", err)
		}
	}

	err = table.Flush()
	if err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}

	return nil
}
