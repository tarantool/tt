package registry

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// tabColumnPadding is the gap between table columns.
const tabColumnPadding = 2

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
