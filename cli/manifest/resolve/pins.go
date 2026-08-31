package resolve

import (
	"maps"

	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/tt/cli/manifest"
)

// Pins maps a rock name to the exact version resolution should prefer. It is
// how "keep what the lock already chose" is expressed.
//
// Without it every re-resolution takes the newest version a constraint allows,
// and since editing the manifest changes manifest_hash - which forces a
// re-resolve - `tt package add` or `remove` would drag every unrelated
// dependency forward. That contradicts the rule IsStale states: new registry
// versions never make a lock stale, only an explicit `tt package update` pulls
// them. Pins are what make that rule implementable, and they are also what
// distinguishes `tt package update <name>` (re-resolve one rock, hold the rest)
// from a bare `tt package update`.
//
// A pin is a preference, not a constraint. Where the pinned version no longer
// fits - the manifest's constraints changed, a transitive edge demands more, or
// the registry stopped serving that version - the pin is dropped, the rock is
// resolved the ordinary newest-that-fits way and a warning says so. Resolution
// never fails because of a pin.
type Pins map[string]string

// PinsFromLock collects the registry-sourced dependencies of every product in
// lock, and of its dev closure, into a pin set. Names listed in except are left
// out, so the caller can free one dependency while holding the rest - which is
// exactly `tt package update <name>`. A nil lock yields an empty set.
//
// The dev closure is walked for the same reason the products are: editing the
// manifest changes manifest_hash and forces a re-resolve, so a dev dependency
// left out of the pin set would be dragged to its newest version by a
// `tt package add` that had nothing to do with it - the very bug pins exist to
// prevent, reintroduced for half the manifest.
//
// Path dependencies are skipped: they are pinned by path and content hash
// already, and the engine never applies a version pin to them.
//
// Products carry independent closures, so two of them may legitimately hold
// different versions of the same rock (different components, different
// constraints). A Pins maps a name to one version, so a disagreement has to be
// decided here, and the highest version wins - deliberately, not by product
// order:
//
//   - Taking the first product's version would silently downgrade every later
//     product that had legitimately resolved higher, on an edit that had
//     nothing to do with that rock. A downgrade is the more damaging of the two
//     outcomes: it can take an API the code already calls away from it.
//   - The highest version instead reproduces each product's own lock entry in
//     the common case. A product whose constraints exclude it does not get
//     dragged up - the pin simply does not fit there, so it is dropped for that
//     product and the rock resolves normally, warning as usual.
//   - It is also stable under renaming a product, which sorted order is not.
//
// A version that does not parse never wins the comparison, so the first one
// seen in sorted-product order is kept for a rock whose lock entries are
// unusable.
func PinsFromLock(lock *manifest.Lock, except ...string) Pins {
	pins := Pins{}
	if lock == nil {
		return pins
	}

	excluded := make(map[string]bool, len(except))
	for _, name := range except {
		excluded[name] = true
	}

	for _, product := range sortedKeys(lock.Products) {
		pins.absorb(lock.Products[product].Dependencies, excluded)
	}

	// The dev closure is absorbed last, but order does not decide anything: the
	// same highest-version-wins tie-break settles a rock that appears in both a
	// product closure and the dev closure at different versions, for the reason
	// spelled out above - a downgrade is the more damaging outcome.
	pins.absorb(lock.DevDependencies, excluded)

	return pins
}

// absorb folds one locked closure into the pin set, skipping path sources,
// versionless entries and excluded names, and keeping the highest version where
// a rock is already pinned.
func (p Pins) absorb(dependencies []manifest.LockDependency, excluded map[string]bool) {
	for _, dependency := range dependencies {
		if dependency.Source != manifestSourceRegistry || dependency.Version == "" {
			continue
		}

		if excluded[dependency.Name] {
			continue
		}

		held, seen := p[dependency.Name]
		if !seen || ordersAbove(dependency.Version, held) {
			p[dependency.Name] = dependency.Version
		}
	}
}

// merge returns a copy of p with other's pins layered on top, other winning
// where both hold a name. It is how the dev resolution states its precedence:
// the runtime picks this pass just made are authoritative over whatever the
// caller was holding, because they are what is already going into the tree.
func (p Pins) merge(other Pins) Pins {
	out := make(Pins, len(p)+len(other))

	maps.Copy(out, p)
	maps.Copy(out, other)

	return out
}

// ordersAbove reports whether candidate sorts strictly above held. An
// unparseable candidate never wins; an unparseable incumbent always loses, so a
// usable version replaces one the engine could not have honored anyway.
func ordersAbove(candidate, held string) bool {
	parsedCandidate, candidateErr := deps.ParseVersion(candidate)
	if candidateErr != nil {
		return false
	}

	parsedHeld, heldErr := deps.ParseVersion(held)
	if heldErr != nil {
		return true
	}

	return deps.Compare(parsedCandidate, parsedHeld) > 0
}

// without returns a copy of pins with name removed. ResolvePinned drops pins as
// it retries, and the caller's set must survive the call unchanged.
func (p Pins) without(name string) Pins {
	out := make(Pins, len(p))

	for rock, version := range p {
		if rock != name {
			out[rock] = version
		}
	}

	return out
}

// pinConflictError reports that a pinned pick, honored earlier in the walk,
// cannot satisfy a requirement discovered later in it. The greedy walk cannot
// un-choose a rock whose subtree it has already resolved, so this is raised as
// a signal rather than handled in place: ResolvePinned catches it, drops that
// one pin and starts the run over, which is what keeps a pin from turning a
// resolvable manifest into an error.
//
// It carries the conflict it would otherwise have been so that, should it ever
// escape the retry loop, it still reads like one and still matches
// errors.Is(err, ErrConflict).
type pinConflictError struct {
	name     string
	conflict *conflictError
}

func (e *pinConflictError) Error() string { return e.conflict.Error() }

func (e *pinConflictError) Unwrap() error { return e.conflict }
