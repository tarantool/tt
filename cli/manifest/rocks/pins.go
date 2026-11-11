package rocks

import (
	"errors"

	luarocks "github.com/tarantool/go-luarocks"
)

// PinAdvice says what to do about err, the failure of an install from a lock
// whose entry pins the digests of the rock's files (client.InstallOpts.Artifacts),
// or returns "" for an error that is not about them. Every install from a
// lock adds it to its error, so the user is told the way out wherever the
// install runs:
//
//   - luarocks.ErrArtifactNotPinned: the lock was resolved where this
//     platform's file was not listed, and resolving it again records it;
//   - luarocks.ErrChecksumMismatch: a file is not the one the lock or the
//     server pins. That is the case the pins are there for, so nothing is
//     installed; a server that published the file anew on purpose has it
//     pinned by resolving again, which is the user's call.
func PinAdvice(err error) string {
	switch {
	case errors.Is(err, luarocks.ErrArtifactNotPinned):
		return "the lock pins no file of the rock for this platform: " +
			"run tt package resolve to pin the files the rock server serves now"
	case errors.Is(err, luarocks.ErrChecksumMismatch):
		return "the file does not have the sha256 the lock or the rock server gives for it, " +
			"so it was not installed; if the rock server published the rock anew on " +
			"purpose, run tt package resolve to pin what it serves now"
	default:
		return ""
	}
}
