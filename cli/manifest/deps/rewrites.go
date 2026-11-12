package deps

import (
	"bytes"
	"fmt"
	"maps"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// lockRewrites says what the lock after changes against before, nil when
// there was none, beyond moves: one note for each change, in a fixed order -
// the fields of the lock, then the closures, products by name and the dev
// closure last. written and source are the bytes of after and of before,
// which settle the rest: a lock that differs in nothing the notes name but
// not byte for byte - laid out by hand, or with a field this tt does not
// write - is said to be rewritten.
//
// It is what keeps a run that moved no rock from saying that the lock is up
// to date when it rewrote it: a resolve rewrites manifest_hash after an edit
// that changed no dependency, and pins anew a path dependency whose files
// changed, and neither moves a version. Nor does a path dependency without a
// version, a directory of files only, which moves never report.
func lockRewrites(before *manifest.Lock, source []byte,
	after *manifest.Lock, written []byte, moves []Move,
) []string {
	var notes []string

	first := before == nil
	if first {
		// The arrivals of the moves say that the lock is new, when there are
		// any.
		if len(moves) == 0 {
			notes = append(notes, "lock written; there was none")
		}

		before = &manifest.Lock{}
	} else {
		notes = fieldRewrites(before, after)
	}

	moved := make(map[string]bool, len(moves))
	for _, move := range moves {
		moved[move.Name] = true
	}

	products := maps.Clone(before.Products)
	if products == nil {
		products = map[string]manifest.LockProduct{}
	}

	maps.Copy(products, after.Products)

	for _, product := range sortedKeys(products) {
		was, inBefore := before.Products[product]
		now, inAfter := after.Products[product]
		where := fmt.Sprintf("product %q", product)

		// A product added or dropped is one note; the rocks of a first lock
		// that moves do not report are named.
		switch {
		case !inBefore && !first:
			notes = append(notes, where+" added")
		case !inAfter:
			notes = append(notes, where+" dropped")
		default:
			notes = append(notes,
				closureRewrites(where, was.Dependencies, now.Dependencies, moved)...)
		}
	}

	notes = append(notes, closureRewrites("the dev dependencies",
		before.DevDependencies, after.DevDependencies, moved)...)

	// A path dependency of two products is pinned anew once.
	notes = dedupe(notes)

	if !first && len(notes) == 0 && len(moves) == 0 && !bytes.Equal(source, written) {
		notes = append(notes, "lock rewritten in the form tt writes it")
	}

	return notes
}

// fieldRewrites notes the fields of the lock that changed, manifest_hash
// first: it is what a resolve after an edit of no dependency rewrites.
func fieldRewrites(before, after *manifest.Lock) []string {
	var notes []string

	if before.ManifestHash != after.ManifestHash {
		notes = append(notes, "manifest hash refreshed")
	}

	fields := []struct{ name, was, now string }{
		{"lock_version", before.LockVersion, after.LockVersion},
		{"manifest_version", before.ManifestVersion, after.ManifestVersion},
		{"generated_by", before.GeneratedBy, after.GeneratedBy},
		{"bundled_tarantool_version", before.BundledTarantool, after.BundledTarantool},
		{"bundled_tt_version", before.BundledTt, after.BundledTt},
		{"bundled_tcm_version", before.BundledTcm, after.BundledTcm},
	}

	for _, field := range fields {
		if field.was != field.now {
			notes = append(notes, fmt.Sprintf("%s is %q, was %q", field.name, field.now, field.was))
		}
	}

	return notes
}

// closureRewrites notes what changed in one closure, where, for the rocks
// whose version moves does not report: a rock that joined or left it, a
// version that moved here and not in the closure moves were taken from, and
// what else of an entry was pinned anew.
func closureRewrites(where string, was, now []manifest.LockDependency,
	moved map[string]bool,
) []string {
	old := entriesByName(was)
	current := entriesByName(now)

	names := maps.Clone(old)
	maps.Copy(names, current)

	var notes []string

	for _, name := range sortedKeys(names) {
		if moved[name] {
			continue
		}

		earlier, inOld := old[name]
		later, inNew := current[name]

		switch {
		case !inOld:
			notes = append(notes, fmt.Sprintf("%q joined %s", name, where))
		case !inNew:
			notes = append(notes, fmt.Sprintf("%q left %s", name, where))
		default:
			notes = append(notes, entryRewrites(where, earlier, later)...)
		}
	}

	return notes
}

// entryRewrites notes what changed between two entries of one rock in one
// closure.
func entryRewrites(where string, earlier, later manifest.LockDependency) []string {
	var notes []string

	name := later.Name

	if earlier.Version != later.Version {
		notes = append(notes, fmt.Sprintf("%q %s -> %s in %s",
			name, earlier.Version, later.Version, where))
	}

	switch {
	case earlier.Source != later.Source || earlier.Path != later.Path:
		notes = append(notes, fmt.Sprintf("%q is from %s, was from %s",
			name, sourceOf(later), sourceOf(earlier)))
	case earlier.ContentHash != later.ContentHash:
		notes = append(notes, fmt.Sprintf("path dependency %q re-pinned: its files changed", name))
	}

	if !maps.Equal(earlier.Artifacts, later.Artifacts) {
		notes = append(notes, fmt.Sprintf("file digests of %q re-pinned", name))
	}

	return notes
}

// sourceOf names where a locked rock comes from, for a note.
func sourceOf(dependency manifest.LockDependency) string {
	if dependency.Path != "" {
		return fmt.Sprintf("%s %q", dependency.Source, dependency.Path)
	}

	return dependency.Source
}

// entriesByName indexes a closure by rock name.
func entriesByName(closure []manifest.LockDependency) map[string]manifest.LockDependency {
	out := make(map[string]manifest.LockDependency, len(closure))
	for _, dependency := range closure {
		out[dependency.Name] = dependency
	}

	return out
}

// dedupe drops the notes said before, keeping the first of each.
func dedupe(notes []string) []string {
	seen := make(map[string]bool, len(notes))
	out := notes[:0]

	for _, note := range notes {
		if !seen[note] {
			seen[note] = true
			out = append(out, note)
		}
	}

	return out
}
