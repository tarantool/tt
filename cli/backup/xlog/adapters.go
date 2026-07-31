package xlog

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/google/uuid"

	xdir "github.com/tarantool/go-xlog/dir"
	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/reader"
	"github.com/tarantool/go-xlog/tools"
	"github.com/tarantool/go-xlog/writer"

	"github.com/tarantool/tt/cli/backup"
)

// PatchInstanceUUID rewrites the Instance field in the src header
// writing to dst.
func PatchInstanceUUID(src, dst, newUUID string) error {
	id, err := uuid.Parse(newUUID)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidUUID, newUUID, err)
	}

	if src == dst {
		err := tools.ReplaceInstanceUUIDInPlace(src, id)
		if errors.Is(err, tools.ErrUUIDWidthMismatch) {
			return fmt.Errorf("%w: %q", ErrInPlaceWidthMismatch, src)
		}

		if err != nil {
			return fmt.Errorf("xlog: patch instance uuid in place %q: %w", src, err)
		}

		return nil
	}

	if err := tools.RewriteMeta(src, dst, tools.ReplaceInstanceUUID(id)); err != nil {
		return fmt.Errorf("xlog: patch instance uuid %q to %q: %w", src, dst, err)
	}

	return nil
}

// TruncateAt copies transactions from src to dst up to and including the one
// that reaches the recovery point (replicaID, lsn), and drops everything
// after it. A transaction is never split: one straddling the point is kept
// whole. When no transaction reaches the point, everything is kept.
//
// Truncation is positional, not per-replica. A recovery point marks a place
// in the log, so every transaction ahead of it belongs to the state it
// describes, whichever replica wrote it. A master's journal does carry rows
// from the other members of its replicaset — a vclock like {1: 1502, 2: 230}
// is the normal case — and filtering by "rows of replicaID up to lsn" would
// drop all of those, including ones committed long before the point.
//
// The meta header is carried over verbatim. A journal file's VClock is the
// position it *starts* at, and its name is that vclock's signature, so
// dropping a tail never moves it: rewriting VClock to the kept content's end
// would desynchronize the header from the file name, which dir.OpenDir
// rejects (ErrSignatureMismatch) and Tarantool treats as fatal in
// xdir_open_cursor (src/box/xlog.c:451).
func TruncateAt(src, dst string, replicaID uint32, lsn int64) error {
	meta, err := reader.ReadHeader(src)
	if err != nil {
		return fmt.Errorf("xlog: truncate: read header %q: %w", src, err)
	}

	return copyUntilPoint(src, dst, meta, replicaID, lsn) //nolint:wrapcheck
}

// FindTrimFile returns the .xlog in dir whose transaction stream includes the
// point (replicaID, lsn).
func FindTrimFile(dir string, replicaID uint32, lsn int64) (string, error) {
	d, err := xdir.OpenDir(dir, format.FiletypeXLOG)
	if err != nil {
		return "", fmt.Errorf("xlog: index xlog dir %q: %w", dir, err)
	}

	entry, err := d.LocateLSN(replicaID, lsn)
	if err != nil {
		if errors.Is(err, xdir.ErrNotFound) {
			return "", fmt.Errorf("%w: dir %q replica %d lsn %d",
				ErrTrimFileNotFound, dir, replicaID, lsn)
		}

		return "", fmt.Errorf("xlog: locate trim file in %q: %w", dir, err)
	}

	return entry.Path, nil
}

// SignatureOf returns a journal file's start position: the vclock signature
// its name encodes and its meta header repeats.
func SignatureOf(path string) (int64, error) {
	meta, err := reader.ReadHeader(path)
	if err != nil {
		return 0, fmt.Errorf("xlog: read header %q: %w", path, err)
	}

	return meta.VClock.Signature(), nil
}

// JournalsAfter returns the .snap and .xlog files in dir that start strictly
// after signature — the part of a backup that reaches past a recovery point.
func JournalsAfter(dir string, signature int64) ([]string, error) {
	var after []string

	for _, filetype := range []format.Filetype{format.FiletypeSNAP, format.FiletypeXLOG} {
		d, err := xdir.OpenDir(dir, filetype)
		if err != nil {
			return nil, fmt.Errorf("xlog: index dir %q: %w", dir, err)
		}

		for _, entry := range d.Files() {
			if entry.Signature > signature {
				after = append(after, entry.Path)
			}
		}
	}

	return after, nil
}

// toFormatVClock converts a manifest vclock to format.VClock.
func toFormatVClock(v backup.Vclock) (format.VClock, error) {
	if v == nil {
		return nil, nil
	}

	out := make(format.VClock, len(v))

	for id, lsn := range v {
		if lsn > math.MaxInt64 {
			return nil, fmt.Errorf("%w: replica %d lsn %d exceeds int64", ErrLSNOverflow, id, lsn)
		}

		out[id] = int64(lsn)
	}

	return out, nil
}

// fromFormatVClock is the inverse of toFormatVClock, returning ErrLSNOverflow
// if any LSN is negative.
func fromFormatVClock(v format.VClock) (backup.Vclock, error) {
	if v == nil {
		return nil, nil
	}

	out := make(backup.Vclock, len(v))

	for id, lsn := range v {
		if lsn < 0 {
			return nil, fmt.Errorf("%w: replica %d lsn %d is negative", ErrLSNOverflow, id, lsn)
		}

		out[id] = uint64(lsn)
	}

	return out, nil
}

// copyUntilPoint writes meta to dst and streams transactions from src,
// stopping after the one that reaches the point, and discarding the
// .inprogress file on any error.
//
// pipe.Copy is deliberately not used here: it decides per row and keeps a
// transaction when *any* row matches, which cannot express "stop at this
// position in the log".
func copyUntilPoint(src, dst string, meta *format.Meta, replicaID uint32, lsn int64) error {
	r, err := reader.Open(src)
	if err != nil {
		return fmt.Errorf("xlog: truncate: open %q: %w", src, err)
	}

	defer func() { _ = r.Close() }()

	w, err := writer.Create(dst, meta)
	if err != nil {
		return fmt.Errorf("xlog: truncate: create %q: %w", dst, err)
	}

	committed := false

	defer func() {
		if !committed {
			_ = w.Discard()
		}
	}()

	for tx, txErr := range r.Txs() {
		if txErr != nil {
			return fmt.Errorf("xlog: truncate: read tx from %q: %w", src, txErr)
		}

		if err := w.WriteTx(tx.Rows); err != nil {
			return fmt.Errorf("xlog: truncate: write tx to %q: %w", dst, err)
		}

		if reachesPoint(tx.Rows, replicaID, lsn) {
			break
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("xlog: truncate: finalize %q: %w", dst, err)
	}

	committed = true

	return nil
}

// reachesPoint reports whether a transaction carries the recovery point, i.e.
// holds a row of replicaID at or past lsn. ">=" rather than "==" so a point
// that names an LSN the log skipped still stops the copy instead of running
// off the end of the file.
func reachesPoint(rows []format.XRow, replicaID uint32, lsn int64) bool {
	return slices.ContainsFunc(rows, func(row format.XRow) bool {
		return row.ReplicaID == replicaID && row.LSN >= lsn
	})
}
