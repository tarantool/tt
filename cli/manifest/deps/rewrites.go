package deps

import (
	"bytes"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// lockRewrites says what the lock after changes against before, nil when
// there was none, beyond moves: one note for each change, in a fixed order -
// the fields of the lock, the checksums of a lock of an older tt dropped,
// then the closures, products by name and the dev closure last. written and
// source are the bytes of after and of before, which settle the rest: a lock
// that differs in nothing the notes name but not byte for byte - laid out by
// hand, or with another field this tt does not write - is said to be
// rewritten.
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
		notes = append(notes, checksumRewrites(source, after)...)
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

// legacyLock is what of a lock an older tt wrote the parsed lock does not
// keep: the md5 of the source of a rock under checksum, which tt locks no
// more and drops when it writes the lock again.
type legacyLock struct {
	Lock struct {
		Products map[string]struct {
			Dependencies []legacyEntry `toml:"dependencies"`
		} `toml:"products"`
		DevDependencies []legacyEntry `toml:"dev_dependencies"`
	} `toml:"lock"`
}

// legacyEntry is a lock entry as legacyLock reads it.
type legacyEntry struct {
	Name     string `toml:"name"`
	Checksum string `toml:"checksum"`
}

// checksumRewrites notes the checksum keys of the lock on disk, source, that
// the lock written, after, drops: one note naming every rock it still locks
// whose entry had one, nil for none. A rock the lock no longer holds is
// reported as gone, which says the rest.
func checksumRewrites(source []byte, after *manifest.Lock) []string {
	var legacy legacyLock

	// The lock was parsed from source before, so it decodes; a lock that
	// does not names no checksum to drop.
	if toml.Unmarshal(source, &legacy) != nil {
		return nil
	}

	entries := legacy.Lock.DevDependencies
	for _, product := range legacy.Lock.Products {
		entries = append(entries, product.Dependencies...)
	}

	locked := entriesByName(after.DevDependencies)
	for _, product := range after.Products {
		maps.Copy(locked, entriesByName(product.Dependencies))
	}

	dropped := map[string]bool{}

	for _, entry := range entries {
		if _, ok := locked[entry.Name]; ok && entry.Checksum != "" {
			dropped[entry.Name] = true
		}
	}

	if len(dropped) == 0 {
		return nil
	}

	names := sortedKeys(dropped)

	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = strconv.Quote(name)
	}

	what := "checksum of"
	if len(names) > 1 {
		what = "checksums of"
	}

	return []string{fmt.Sprintf("%s %s dropped: tt no longer locks the md5 of the source of a rock",
		what, strings.Join(quoted, ", "))}
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

	if note := digestRewrite(earlier, later); note != "" {
		notes = append(notes, note)
	}

	return notes
}

// digestRewrite notes what happened to the file digests of one entry, empty
// when they held: pinned when the lock had none, dropped - for a server that
// lists none, a rock index server left for a plain one, or for a path
// dependency, pinned by its files instead - or pinned anew.
func digestRewrite(earlier, later manifest.LockDependency) string {
	name := later.Name

	switch {
	case maps.Equal(earlier.Artifacts, later.Artifacts):
		return ""
	case len(earlier.Artifacts) == 0:
		return fmt.Sprintf("file digests of %q pinned; the lock had none", name)
	case len(later.Artifacts) == 0 && later.Path != "":
		return fmt.Sprintf(
			"file digests of %q dropped: a path dependency is pinned by content_hash", name)
	case len(later.Artifacts) == 0:
		return fmt.Sprintf("file digests of %q dropped: its server lists none", name)
	default:
		return fmt.Sprintf("file digests of %q pinned anew", name)
	}
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
