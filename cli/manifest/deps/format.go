package deps

import (
	"encoding/json"
	"errors"
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

// devProduct is what the dev closure is called in the human table's first
// column. It is not a product — the manifest declares dev dependencies
// globally — so it is spelled differently from every product name, which the
// name rules make impossible to collide with.
const devProduct = "(dev)"

// ErrUnknownFormat reports a -o value that is not one of the three formats.
var ErrUnknownFormat = errors.New("unknown output format")

// Format is how a report is rendered.
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
// An explicit value always wins. With none given the default follows the same
// tt convention tt package list uses: a terminal gets the table, a pipe or a
// file gets YAML, so output being consumed by something else is parseable
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

// Render writes a report in the chosen format. out carries the report itself;
// notes carries the narrative about the lock, so a redirected table is the
// table and nothing else. The machine formats put the lock state in the
// document and write nothing to notes.
func Render(out, notes io.Writer, report *Report, format Format) error {
	switch format {
	case FormatJSON:
		return renderJSON(out, report)
	case FormatYAML:
		return renderYAML(out, report)
	case FormatTable:
		return renderTable(out, notes, report)
	default:
		return stateErrorf("%w %q", ErrUnknownFormat, format)
	}
}

// renderJSON writes indented JSON with a trailing newline, so the output is
// pleasant both piped into jq and read directly.
func renderJSON(out io.Writer, report *Report) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")

	err := encoder.Encode(report)
	if err != nil {
		return fmt.Errorf("rendering JSON: %w", err)
	}

	return nil
}

// renderYAML writes YAML at tt's usual two-space indent.
func renderYAML(out io.Writer, report *Report) error {
	encoder := yaml.NewEncoder(out)
	encoder.SetIndent(yamlIndent)

	err := encoder.Encode(report)
	if err != nil {
		return fmt.Errorf("rendering YAML: %w", err)
	}

	return encoder.Close() //nolint:wrapcheck // Close reports the same encode error.
}

// renderTable writes the human-readable report: one row per dependency, with
// the product (or the dev closure) it belongs to in the first column.
//
// The lock state is a line on notes rather than a column, because it qualifies
// every version below it at once: a stale lock means the whole VERSION column
// is what the last resolution chose, not what the next one will. A manifest
// that declares nothing gets a sentence instead of a bare header, which would
// read like a bug.
func renderTable(out, notes io.Writer, report *Report) error {
	err := renderLockLine(notes, report)
	if err != nil {
		return err
	}

	rows := tableRows(report)
	if len(rows) == 0 {
		_, err = fmt.Fprintf(out, "%s declares no dependencies\n", report.Package)
		if err != nil {
			return fmt.Errorf("rendering table: %w", err)
		}

		return nil
	}

	table := tabwriter.NewWriter(out, 0, 0, tabColumnPadding, ' ', 0)

	_, err = fmt.Fprintln(table, "PRODUCT\tNAME\tCONSTRAINT\tVERSION\tSOURCE\tORIGIN")
	if err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}

	for _, row := range rows {
		_, err = fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
			dash(row.product), row.entry.Name, dash(row.entry.Constraint),
			dash(row.entry.Version), dash(row.entry.Source), originOf(row.entry))
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

// renderLockLine states what the versions below are worth, and says how to fix
// a lock that cannot answer the question.
func renderLockLine(out io.Writer, report *Report) error {
	var line string

	switch report.Lock {
	case LockMissing:
		line = "no lock yet: run tt package resolve to pin versions\n\n"
	case LockStale:
		line = fmt.Sprintf(
			"lock is stale (%s): the versions below are the last resolved ones;"+
				" run tt package resolve\n\n", report.LockReason)
	case LockCurrent:
		line = ""
	default:
		line = ""
	}

	if line == "" {
		return nil
	}

	_, err := fmt.Fprint(out, line)
	if err != nil {
		return fmt.Errorf("rendering table: %w", err)
	}

	return nil
}

// tableRow is one rendered line: an entry plus the closure it came from.
type tableRow struct {
	product string
	entry   Entry
}

// tableRows flattens the report into rows, products in report order and the dev
// closure last.
func tableRows(report *Report) []tableRow {
	var rows []tableRow

	for _, product := range report.Products {
		for _, entry := range product.Dependencies {
			rows = append(rows, tableRow{product: product.Name, entry: entry})
		}
	}

	for _, entry := range report.DevDependencies {
		rows = append(rows, tableRow{product: devProduct, entry: entry})
	}

	return rows
}

// originOf labels a row as declared by the manifest or pulled in behind a
// declaration.
func originOf(entry Entry) string {
	if entry.Direct {
		return "direct"
	}

	return "transitive"
}

// dash renders an empty field as "-", so a column never looks truncated.
func dash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
