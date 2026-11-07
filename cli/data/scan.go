package data

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"

	"github.com/tarantool/tt/sdk/log"
)

// Row is a row of a journal file, with the body of a data change decoded.
type Row struct {
	format.XRow

	// DML is the body of a data change of a space - INSERT, REPLACE,
	// UPDATE, DELETE, UPSERT, INSERT_ARROW - and nil for any other row. The rows of a
	// vinyl log are INSERTs of no space and have none.
	DML *format.DMLBody
	// Stmt is the body of a statement of a vinyl run - INSERT, REPLACE,
	// DELETE, UPSERT - and nil for any other row.
	Stmt *format.VyStmtBody
}

// Txn is a transaction a filter selects, with the rows of it the filter
// keeps. A row of a snapshot, of a vinyl log or of a vinyl run is a
// transaction of its own.
type Txn struct {
	// File is the file the transaction is in.
	File *File
	// Rows are the rows the filter keeps, in their order in the file; there
	// is at least one.
	Rows []Row
}

// ScanOptions are how Scan reads damaged files.
type ScanOptions struct {
	// SkipCorrupt skips a block of transactions whose checksum does not
	// match, as Tarantool does with force_recovery, instead of failing.
	SkipCorrupt bool
}

// Scan reads the files of plan and calls fn with each transaction filter
// selects, in the order of the files. It stops at the first error of fn.
//
// The end of a write-ahead log an instance is still writing is read as far
// as it goes: a file without the end marker is not an error, and neither is
// an incomplete transaction at its end, which is left out.
func Scan(plan *Plan, filter *Filter, opts ScanOptions, fn func(*Txn) error) error {
	for _, file := range plan.Files {
		log.Debugf("Reading the %s %s", file.Kind, file.Path)

		err := scanFile(plan, file, filter, opts, fn)
		if err != nil {
			return err
		}
	}

	return nil
}

func scanFile(plan *Plan, file *File, filter *Filter, opts ScanOptions,
	visit func(*Txn) error,
) error {
	options := []reader.Option{reader.AcceptV012(), reader.IgnoreMissingEOF()}
	if opts.SkipCorrupt {
		options = append(options, reader.SkipCorruptTx())
	}

	journal, err := reader.Open(file.Path, options...)
	if err != nil {
		return fmt.Errorf("reading %s: %w", file.Path, err)
	}

	defer func() { _ = journal.Close() }()

	for {
		journalTx, err := journal.NextTx()

		switch {
		case errors.Is(err, io.EOF):
			if !journal.SawEOFMarker() {
				log.Infof("%s has no end marker: an instance is writing it, "+
					"or stopped before it closed it", file.Path)
			}

			return nil
		case errors.Is(err, reader.ErrTruncated):
			log.Warnf("%s ends inside a transaction, which is left out: an instance is "+
				"writing it, or stopped before it finished the transaction", file.Path)

			return nil
		case err != nil:
			return fmt.Errorf("reading %s: %w", file.Path, err)
		}

		txn, err := selectTxn(plan, file, filter, journalTx.Rows)
		if err != nil {
			return err
		}

		if txn == nil {
			continue
		}

		err = visit(txn)
		if err != nil {
			return err
		}
	}
}

// selectTxn returns the transaction of rows that filter selects, with the
// rows it keeps, or nil when it selects none of them.
func selectTxn(plan *Plan, file *File, filter *Filter, rows []format.XRow) (*Txn, error) {
	if file.Kind == KindXlog {
		selected, err := selectsXlogTxn(plan, file, filter, rows)
		if err != nil {
			return nil, err
		}

		if !selected {
			return nil, nil //nolint:nilnil // No transaction is selected.
		}
	}

	txn := &Txn{File: file, Rows: make([]Row, 0, len(rows))}

	for _, xrow := range rows {
		row := Row{XRow: xrow, DML: nil, Stmt: nil}

		switch {
		case file.Kind == KindRun && isStatement(xrow.Type):
			stmt, err := format.DecodeVyStmtBody(xrow.BodyRaw)
			if err != nil {
				log.Warnf("%s: the body of the %s at LSN %d does not decode: %s",
					file.Path, format.TypeName(xrow.Type), xrow.LSN, err)
			} else {
				row.Stmt = stmt
			}
		case isDML(xrow.Type) && (file.Kind == KindSnap || file.Kind == KindXlog):
			body, err := format.DecodeDMLBody(xrow.BodyRaw)
			if err != nil {
				log.Warnf("%s: the body of the %s at LSN %d does not decode: %s",
					file.Path, format.TypeName(xrow.Type), xrow.LSN, err)
			} else {
				row.DML = body
			}
		}

		if filter.keepRow(file, &row) {
			txn.Rows = append(txn.Rows, row)
		}
	}

	if len(txn.Rows) == 0 {
		return nil, nil //nolint:nilnil // No transaction is selected.
	}

	return txn, nil
}

// selectsXlogTxn reports whether filter selects the transaction of rows of a
// write-ahead log: one the snapshot of plan does not contain, within the
// bounds.
func selectsXlogTxn(plan *Plan, file *File, filter *Filter, rows []format.XRow) (bool, error) {
	if covers(plan.Base, file) && contained(rows, plan.Base.Meta.VClock) {
		return false, nil
	}

	inRange := !filter.bounded()

	for _, row := range rows {
		err := filter.bindReplica(row, file.Path)
		if err != nil {
			return false, err
		}

		if !inRange && filter.inRange(row) {
			inRange = true
		}
	}

	if !inRange {
		return false, nil
	}

	return filter.Before == 0 || rows[0].Timestamp < filter.Before, nil
}

// contained reports whether a snapshot at vclock holds the changes of the
// transaction of rows: each of them is at most at its LSN in vclock.
func contained(rows []format.XRow, vclock format.VClock) bool {
	for _, row := range rows {
		if row.LSN > vclock[row.ReplicaID] {
			return false
		}
	}

	return true
}

// formatTime writes seconds since the epoch as an RFC 3339 time in UTC.
func formatTime(seconds float64) string {
	whole, fraction := math.Modf(seconds)

	return time.Unix(int64(whole), int64(fraction*float64(time.Second))).UTC().
		Format(time.RFC3339Nano)
}
