package backup

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

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
