package util

import (
	"errors"
	"regexp"
)

var errCommitHashTooShort = errors.New("the hash must contain at least 7 characters")

// MinCommitHashLength is the Git default for a short SHA.
const MinCommitHashLength = 7

// commitHashPattern matches a full or abbreviated lowercase commit hash.
var commitHashPattern = regexp.MustCompile(`^[0-9a-f]+$`)

// IsValidCommitHash checks hash format.
func IsValidCommitHash(hash string) (bool, error) {
	if len(hash) < MinCommitHashLength {
		return false, errCommitHashTooShort
	}

	return commitHashPattern.MatchString(hash), nil
}
