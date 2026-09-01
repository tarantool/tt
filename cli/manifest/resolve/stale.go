package resolve

import (
	"fmt"
	"path/filepath"

	"github.com/tarantool/tt/cli/manifest"
)

// IsStale reports whether lock no longer reflects m, and why. A lock goes stale
// from exactly three things: the manifest's raw bytes changed (manifest_hash
// diverged), a path dependency's local content changed (its content_hash
// diverged), or the manifest declares dev dependencies the lock holds no
// closure for. New registry versions never make a lock stale - only an explicit
// tt package update pulls them; a changed tt version or package VERSION does
// not either.
//
// The third case is not a change in the project at all but a change in tt: a
// lock written before tt resolved [dev_dependencies] carries none, and its
// manifest_hash still matches because the manifest never changed. Without this
// case such a project would silently never get its dev dependencies again -
// nothing else can notice, because the only evidence is the lock's own
// emptiness. The check is one-directional on purpose: a dev closure in the lock
// for a manifest that declares none is not staleness (nothing to install), and
// dev picks that merely aged are not either, by the rule above.
//
// What to do with a stale lock is the caller's call: an unflagged build
// re-resolves and rewrites it (Engine.Resolve); a --locked build treats
// staleness as a hard error. The engine only reports the fact.
func (e *Engine) IsStale(man *manifest.Manifest, lock *manifest.Lock) (bool, string, error) {
	return IsStale(e.projectDir, man, lock)
}

// IsStale is Engine.IsStale without an engine: the check reads the manifest's
// hash and the path dependencies' contents under projectDir, and queries no
// registry at all.
//
// It is exported in this form for tt package deps, which reports the lock's
// versions and has to say whether they still stand. That command builds no
// adapter — it neither resolves nor fetches — and constructing an engine around
// a nil one to reach a method that never touches it is how a later change to
// the engine turns into a nil dereference in a read-only command.
func IsStale(
	projectDir string, man *manifest.Manifest, lock *manifest.Lock,
) (bool, string, error) {
	if lock.ManifestHash != man.Hash() {
		return true, "manifest changed since the lock was written", nil
	}

	if len(man.DevDependencies) > 0 && len(lock.DevDependencies) == 0 {
		return true, "manifest declares dev dependencies the lock has no closure for", nil
	}

	// A path dependency shared by several products, or by a product and the dev
	// closure, is hashed once rather than once per closure referencing it.
	hashes := map[string]string{}

	closures := make([][]manifest.LockDependency, 0, len(lock.Products)+1)
	for _, product := range sortedKeys(lock.Products) {
		closures = append(closures, lock.Products[product].Dependencies)
	}

	// Dev path dependencies go through the same content hashing: a dev-only
	// helper directory that changed has to re-resolve exactly like a runtime one.
	closures = append(closures, lock.DevDependencies)

	for _, closure := range closures {
		for _, dependency := range closure {
			if dependency.Source != manifestSourcePath {
				continue
			}

			hash, cached := hashes[dependency.Path]
			if !cached {
				computed, err := contentHash(filepath.Join(projectDir, dependency.Path))
				if err != nil {
					return false, "", fmt.Errorf(
						"checking path dependency %q: %w", dependency.Name, err)
				}

				hash = computed
				hashes[dependency.Path] = computed
			}

			if hash != dependency.ContentHash {
				return true, fmt.Sprintf("path dependency %q changed on disk", dependency.Name), nil
			}
		}
	}

	return false, "", nil
}
