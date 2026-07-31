package xlog

import (
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"

	xdir "github.com/tarantool/go-xlog/dir"
	"github.com/tarantool/go-xlog/filter"
	"github.com/tarantool/go-xlog/format"
	"github.com/tarantool/go-xlog/pipe"
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

// TruncateAt copies transactions from src to dst up to and including the
// recovery point, dropping the rest.
//
// The meta header is carried over verbatim. A journal file's VClock is the
// position it *starts* at, and its name is that vclock's signature, so
// dropping a tail never moves it: rewriting VClock to the kept content's end
// would desynchronize the header from the file name, which dir.OpenDir
// rejects (ErrSignatureMismatch) and Tarantool treats as fatal in
// xdir_open_cursor (src/box/xlog.c:451).
func TruncateAt(src, dst string, replicaID uint32, lsn int64) error {
	pred := filter.UntilVClock(format.VClock{replicaID: lsn})

	meta, err := reader.ReadHeader(src)
	if err != nil {
		return fmt.Errorf("xlog: truncate: read header %q: %w", src, err)
	}

	return copyKeptTxs(src, dst, meta, pred)
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

// copyKeptTxs writes meta to dst and streams every transaction from src that
// passes pred, discarding the .inprogress file on any error.
func copyKeptTxs(src, dst string, meta *format.Meta, pred filter.Filter) error {
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

	if _, err := pipe.Copy(r, w, pred); err != nil {
		return fmt.Errorf("xlog: truncate: copy %q to %q: %w", src, dst, err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("xlog: truncate: finalize %q: %w", dst, err)
	}

	committed = true

	return nil
}
