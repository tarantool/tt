package backup

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// BackupIDLayout is the time layout of the ids NewBackupID produces: a
// zero-padded UTC timestamp, which sorts as text in the order it was taken.
const BackupIDLayout = "20060102T150405Z"

// NewBackupID returns an id for a backup taken now: the current UTC second in
// BackupIDLayout.
//
// It returns only once that second is over, which is what keeps ids unique
// without widening the layout: the next call on this host reads a later second,
// so two ids taken one after the other never collide and sort in the order they
// were taken. The wait is under a second.
//
// Uniqueness holds for sequential calls on one host only. Calls running
// concurrently within one second, calls on different hosts, and a wall clock
// stepped back all can return the same id: upload refuses such a duplicate, but
// start and finalize take the id as a directory name and do not.
//
// A finer layout cannot replace the wait:
// "20260918T143000.123Z" sorts below "20260918T143000Z", out of order with ids
// already stored at second precision.
func NewBackupID() string {
	return newBackupID(time.Now, time.Sleep)
}

// newBackupID is NewBackupID over an injected clock.
func newBackupID(now func() time.Time, sleep func(time.Duration)) string {
	second := now().UTC().Truncate(time.Second)
	next := second.Add(time.Second)

	// Truncate drops the monotonic reading, so this compares wall clocks: the
	// next id is read from the wall clock, and a slewed wall clock can lag a
	// monotonic sleep of the same length.
	for wait := next.Sub(now()); wait > 0; wait = next.Sub(now()) {
		sleep(wait)
	}

	return second.Format(BackupIDLayout)
}

// ErrInvalidBackupID is wrapped by every backup id rejection, so a caller can
// tell a malformed identifier from a runtime failure.
var ErrInvalidBackupID = errors.New("invalid backup id")

// ValidateBackupID checks that id is usable in the three roles it carries: a
// single path component under the local backup root (start, finalize), a
// storage key component (upload), and the sort key that decides which backup is
// the latest (last, verify, gc). Ids come from job names, k8s annotations and
// date templates, so one that addresses a path other than its own has to be
// refused before the instance or the storage is touched.
//
// The rules are a denylist on purpose: orchestrators emit timestamps
// (20260326T120000Z), dashed ids (2026-01-01-full) and non-ASCII names, and all
// of those stay valid.
//
// What this cannot check is the one property the storage layout rests on: ids
// have to sort, as text, in the order the backups are taken. A single id says
// nothing about that, so the storage is where it is enforced -- `upload`
// refuses an id that does not sort above the newest stored backup, which is
// what a bare counter (backup-2 then backup-10) produces. A zero-padded UTC
// timestamp satisfies it by construction.
func ValidateBackupID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("%w %q: must not be empty", ErrInvalidBackupID, id)
	// A leading separator is a separator too, so absolute paths land here.
	case strings.ContainsAny(id, `/\`):
		return fmt.Errorf("%w %q: must not contain a path separator", ErrInvalidBackupID, id)
	// Covers "." and ".." along with ids a plain ls does not show.
	case strings.HasPrefix(id, "."):
		return fmt.Errorf("%w %q: must not start with a dot", ErrInvalidBackupID, id)
	case strings.ContainsFunc(id, unicode.IsControl):
		return fmt.Errorf("%w %q: must not contain control characters", ErrInvalidBackupID, id)
	}

	return nil
}
