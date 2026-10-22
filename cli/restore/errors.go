package restore

import "errors"

var (
	// ErrValidation marks rejected restore input. Checksums, archive entry names
	// and archive-chain continuity are checked before clearing data directories.
	// Other checks, including the restored membership and identity, run after
	// unpacking and before a successful restore is recorded. Callers must not
	// assume that every validation error leaves previous data intact.
	ErrValidation = errors.New("restore: invalid input")

	// ErrNoTrimFile marks a recovery point that no unpacked xlog covers.
	// It is separate from a generic failure because it means the point and
	// the archives disagree, not that the node is broken.
	ErrNoTrimFile = errors.New("restore: no xlog covers the recovery point")
)
