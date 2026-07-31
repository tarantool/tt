package restore

import "errors"

var (
	// ErrValidation marks a rejected input: a checksum that does not match
	// its archive, a malformed recovery point, a missing archive, a chain
	// whose archives do not continue one another. Apply reports all of them
	// but the chain before it touches the work directory, so a previous
	// attempt's files are still intact when it is returned. A chain is only
	// known to be one once its archives have been read through, so that
	// rejection comes mid-run -- without a marker, and a re-run in the right
	// order rebuilds the directory.
	ErrValidation = errors.New("restore: invalid input")

	// ErrNoTrimFile marks a recovery point that no unpacked xlog covers.
	// It is separate from a generic failure because it means the point and
	// the archives disagree, not that the node is broken.
	ErrNoTrimFile = errors.New("restore: no xlog covers the recovery point")
)
