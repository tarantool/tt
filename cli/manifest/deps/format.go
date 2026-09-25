package deps

import (
	"fmt"
	"io"
	"log/slog"
	"text/tabwriter"

	"github.com/tarantool/tt/sdk/log"
)

// tabColumnPadding is the gap between table columns.
const tabColumnPadding = 2

// devProduct is what the dev closure is called in the human table's first
// column. It is not a product — the manifest declares dev dependencies
// globally — so it is spelled differently from every product name, which the
// name rules make impossible to collide with.
const devProduct = "(dev)"

// Human writes the human-readable report: one row per dependency, with the
// product (or the dev closure) it belongs to in the first column. The
// machine formats encode the Report itself, lock state included.
//
// The lock state is logged rather than printed: it qualifies every version at
// once — a stale lock means the whole VERSION column is what the last
// resolution chose, not what the next one will — so it is a note about the
// table, and a redirected table stays the table and nothing else. A manifest
// that declares nothing gets a sentence instead of a bare header, which would
// read like a bug.
func (r *Report) Human(out io.Writer) error {
	return r.human(out, log.Logger())
}

// human is Human with the logger the lock note goes to.
func (r *Report) human(out io.Writer, logger *slog.Logger) error {
	logLockNote(logger, r)

	rows := tableRows(r)
	if len(rows) == 0 {
		_, err := fmt.Fprintf(out, "%s declares no dependencies\n", r.Package)
		if err != nil {
			return fmt.Errorf("rendering table: %w", err)
		}

		return nil
	}

	table := tabwriter.NewWriter(out, 0, 0, tabColumnPadding, ' ', 0)

	_, err := fmt.Fprintln(table, "PRODUCT\tNAME\tCONSTRAINT\tVERSION\tSOURCE\tORIGIN")
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

// logLockNote states what the versions in the table are worth, and says how
// to fix a lock that cannot answer the question. A stale lock is a warning:
// the versions shown are not what the next resolution will choose. A missing
// one is the normal state of a project that has not resolved yet.
func logLockNote(logger *slog.Logger, report *Report) {
	switch report.Lock {
	case LockMissing:
		logger.Info("no lock yet: run tt package resolve to pin versions")
	case LockStale:
		logger.Warn("lock is stale (" + report.LockReason + "): the versions shown are" +
			" the last resolved ones; run tt package resolve")
	case LockCurrent:
	}
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
