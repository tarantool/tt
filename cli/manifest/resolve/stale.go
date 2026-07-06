package resolve

import (
	"fmt"
	"path/filepath"

	"github.com/tarantool/tt/cli/manifest"
)

// IsStale reports whether lock no longer reflects m, and why. A lock goes stale
// from exactly two things: the manifest's raw bytes changed (manifest_hash
// diverged), or a path dependency's local content changed (its content_hash
// diverged). New registry versions never make a lock stale - only an explicit
// tt package update pulls them; a changed tt version or package VERSION does
// not either.
//
// What to do with a stale lock is the caller's call: an unflagged build
// re-resolves and rewrites it (Engine.Resolve); a --locked build treats
// staleness as a hard error. The engine only reports the fact.
func (e *Engine) IsStale(man *manifest.Manifest, lock *manifest.Lock) (bool, string, error) {
	if lock.ManifestHash != man.Hash() {
		return true, "manifest changed since the lock was written", nil
	}

	for _, product := range sortedKeys(lock.Products) {
		for _, dependency := range lock.Products[product].Dependencies {
			if dependency.Source != manifestSourcePath {
				continue
			}

			hash, err := contentHash(filepath.Join(e.projectDir, dependency.Path))
			if err != nil {
				return false, "", fmt.Errorf(
					"checking path dependency %q: %w", dependency.Name, err)
			}

			if hash != dependency.ContentHash {
				return true, fmt.Sprintf("path dependency %q changed on disk", dependency.Name), nil
			}
		}
	}

	return false, "", nil
}
