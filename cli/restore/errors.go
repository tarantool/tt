package restore

import "errors"

var (
	// ErrValidation marks a rejected input: a checksum that does not match
	// its archive, a malformed recovery point, a missing archive, a chain
	// whose archives do not continue one another. Apply reports every one of
	// them before it touches the work directory, so a previous attempt's files
	// are still intact when it is returned -- the guarantee an orchestrator
	// retries on.
	ErrValidation = errors.New("restore: invalid input")

	// ErrNoTrimFile marks a recovery point that no unpacked xlog covers.
	// It is separate from a generic failure because it means the point and
	// the archives disagree, not that the node is broken.
	ErrNoTrimFile = errors.New("restore: no xlog covers the recovery point")
)
