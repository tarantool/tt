package resolve

import (
	"fmt"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// IsStale reports whether lock no longer reflects m, and why. A lock goes stale
// from exactly five things: what the manifest says changed (manifest_hash
// diverged; comments and formatting do not count, see manifest.HashTOML, and a
// hash of another scheme - an older tt's, or one this tt does not know - is
// one that diverged), a path dependency's local content changed (its content_hash
// diverged), the version of a path dependency holding a manifest moved (see
// pathStale), the manifest declares dev dependencies the lock holds no closure
// for, or one of the manifest's effective products has no closure in the lock.
// New registry versions never make a lock stale - only an explicit tt package
// update pulls them; a changed tt version or package VERSION does not either.
//
// The last two cases are not a change in the project at all but a change in
// tt: a lock written before tt resolved [dev_dependencies], or before a
// manifest without [products] resolved into its implicit product, carries
// nothing for them, and its manifest_hash still matches because the manifest
// never changed. Without these cases such a project would silently never get
// those dependencies again - nothing else can notice, because the only
// evidence is the lock's own emptiness. The checks are one-directional on
// purpose: a closure in the lock the manifest no longer asks for is not
// staleness (nothing to install), and picks that merely aged are not either, by
// the rule above.
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
	hashErr := man.CheckHash(lock.ManifestHash)
	if hashErr != nil {
		// A hash that does not name the manifest is the answer, not a failure
		// to find it.
		return true, hashErr.Error(), nil //nolint:nilerr // See above.
	}

	if len(man.DevDependencies) > 0 && len(lock.DevDependencies) == 0 {
		return true, "manifest declares dev dependencies the lock has no closure for", nil
	}

	// The same holds for a product: a lock written while a manifest without
	// [products] resolved into no product at all carries no closure for the
	// implicit one, and its hash still matches.
	for _, product := range sortedKeys(man.EffectiveProducts()) {
		if _, locked := lock.Products[product]; !locked {
			return true, fmt.Sprintf("the lock holds no closure for product %q", product), nil
		}
	}

	closures := make([][]manifest.LockDependency, 0, len(lock.Products)+1)
	for _, product := range sortedKeys(lock.Products) {
		closures = append(closures, lock.Products[product].Dependencies)
	}

	// Dev path dependencies go through the same content hashing: a dev-only
	// helper directory that changed has to re-resolve exactly like a runtime one.
	closures = append(closures, lock.DevDependencies)

	// A path dependency shared by several products, or by a product and the dev
	// closure, is checked once rather than once per closure referencing it.
	checked := map[string]bool{}

	for _, closure := range closures {
		for _, dependency := range closure {
			key := dependency.Name + "\x00" + dependency.Path
			if dependency.Source != manifestSourcePath || checked[key] {
				continue
			}

			checked[key] = true

			reason, err := pathStale(projectDir, dependency)
			if err != nil || reason != "" {
				return reason != "", reason, err
			}
		}
	}

	return false, "", nil
}

// pathStale reports why the lock entry dependency of a path dependency no
// longer reflects its directory, or "" when it does: its content hash
// diverged (pathContentHash), or, for one holding a manifest, its version
// moved - a commit or a tag changes the version git derives without changing a
// file the hash covers, and the lock has to name the version of the rock that
// gets installed.
func pathStale(projectDir string, dependency manifest.LockDependency) (string, error) {
	dir := manifest.PathFrom(projectDir, dependency.Path)

	hash, err := pathContentHash(dir)
	if err != nil {
		return "", fmt.Errorf("checking path dependency %q: %w", dependency.Name, err)
	}

	if hash != dependency.ContentHash {
		return fmt.Sprintf("path dependency %q changed on disk", dependency.Name), nil
	}

	isManifest, err := IsPathManifest(dir)
	if err != nil || !isManifest {
		return "", err
	}

	neighbour, err := LoadPathManifest(dependency.Name, dir)
	if err != nil {
		return "", err
	}

	_, rockVersion, err := neighbour.Version()
	if err != nil {
		return "", err
	}

	if rockVersion != dependency.Version {
		return fmt.Sprintf("path dependency %q is at version %s now, the lock has %s",
			dependency.Name, rockVersion, dependency.Version), nil
	}

	return "", nil
}
