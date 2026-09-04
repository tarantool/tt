// Package deps implements the commands that work on what a package depends on.
//
// Four of them write the lock: tt package add, tt package remove and
// tt package update, plus tt package resolve, which is the same transaction
// with no manifest edit in front of it. Each is the two-step transaction — edit
// the declaration, then re-resolve the lock — and the whole package exists to
// make those two steps agree. The fifth, tt package deps, only reads: it
// reports the declarations and the versions the lock resolved them to, without
// touching a registry (see Deps).
//
// The manifest half is done by the position-preserving editor in
// cli/manifest (NewEditor): app.manifest.toml is hand-authored, so an edit
// splices the one entry it touches and leaves every other byte, comment and
// blank line exactly as it was. The lock half is cli/manifest/resolve. Nothing
// here talks to a registry itself, materializes .rocks/ or runs a build: a
// changed lock is picked up by the next tt package build.
//
// # What separates the three commands is the pin set
//
// Editing the manifest changes manifest_hash, which makes the lock stale, which
// forces a re-resolution — and an unpinned re-resolution takes the newest
// version every constraint allows, dragging every unrelated rock forward on an
// edit that had nothing to do with it. resolve.Pins is what prevents that, and
// choosing the pin set is the whole of what distinguishes the commands:
//
//   - Add, Remove and Resolve pin everything the lock already holds. The rock
//     being added is not in the lock, so it resolves fresh; every other rock
//     keeps the version it had.
//   - Update with no argument pins nothing. It is the only command that pulls
//     newer registry versions, which is exactly the rule resolve.IsStale
//     states.
//   - Update with a name pins everything except that name, so one rock moves
//     and the rest hold.
//
// A missing lock — a project that has never been built — is not an error: there
// is nothing to hold, so the resolution runs unpinned and writes the first
// lock.
//
// # The manifest is written before the lock is resolved
//
// The edit the user asked for reaches disk first, the way cargo does it, and
// only then is the closure resolved. The alternative — hold both in memory and
// write them together — would make a failed resolution leave no trace, which
// reads as "the command did nothing" when what actually happened is "your
// dependency is unsatisfiable". Writing first keeps the request visible and
// editable: the user fixes the constraint in place and runs the command again.
//
// The cost is that a failed resolution leaves the two files disagreeing, and
// every later command will re-resolve and fail identically until the manifest
// is fixed. So that failure says so in as many words — see ErrManifestEdited;
// a user who does not know the manifest changed cannot make sense of the next
// command's error.
package deps

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
	"github.com/tarantool/tt/cli/manifest/rocks"
)

// rocksDirName is the project-scope tree root the rocks adapter is bound to.
// Nothing here materializes into it; the adapter needs a tree to be configured
// against, and it must be the one a following build uses.
const rocksDirName = ".rocks"

// defaultConstraint is what tt package add declares when the user names no
// version: any version the registry serves.
const defaultConstraint = "*"

// defaultFilePerm is the mode a manifest is created with when its current mode
// cannot be read. An existing manifest keeps the mode it has.
const defaultFilePerm os.FileMode = 0o644

// Options configures one dependency-changing run.
type Options struct {
	// ProjectDir is the directory holding app.manifest.toml and, next to it,
	// app.manifest.lock. Required and must be absolute.
	ProjectDir string
	// TtVersion is the "tt x.y.z" string stamped into the rewritten lock's
	// generated_by field.
	TtVersion string
	// Tarantool carries the Tarantool facts the rocks adapter needs. Resolution
	// evaluates rockspecs, which are Lua and can branch on the Tarantool
	// version, so this is required even though nothing is built here.
	Tarantool rocks.TarantoolInfo
	// Servers overrides the rock-server list; nil uses the adapter default.
	Servers []string
	// Logger receives the adapter's structured operation logs; nil disables it.
	Logger *slog.Logger
	// Warn receives non-fatal diagnostics: parse and validation warnings, and
	// the resolver's report of a pin it had to drop. The library never logs
	// directly; the CLI routes these through tt's logger. Nil drops them.
	Warn func(string)
}

// emit surfaces non-fatal diagnostics through the Warn sink, if any.
func (o Options) emit(warnings []string) {
	if o.Warn == nil {
		return
	}

	for _, w := range warnings {
		o.Warn(w)
	}
}

// Move is one rock whose place in the locked closure changed.
//
// An empty From is a rock the closure did not hold before — the added rock
// itself, or one that arrived behind it. An empty To is a rock that dropped out
// of the closure, which is what a remove mostly produces. A rock whose version
// did not move is not reported at all.
type Move struct {
	// Name is the rock name.
	Name string
	// From is the version the previous lock held, empty when it held none.
	From string
	// To is the version the new lock holds, empty when it holds none.
	To string
}

// Result reports what a run changed.
type Result struct {
	// Manifest is the manifest as it now stands on disk: re-parsed from the
	// edited bytes, so its Hash matches what the lock records.
	Manifest *manifest.Manifest
	// Lock is the freshly written lock.
	Lock *manifest.Lock
	// Moves are the closure changes, in name order.
	Moves []Move
	// Change reports what happened to the declaration itself: whether one was
	// already there and, if so, the constraint it carried. It is what lets the
	// CLI say "checks >=3.0.0 -> >=3.1.0" rather than just "checks added", a
	// distinction the moves cannot carry — they are versions, not constraints.
	// A zero Change means the run declared nothing (tt package update).
	Change manifest.Change
}

// resolver is the slice of resolve.Engine these commands drive: re-resolve a
// manifest into a fresh lock, preferring a pin set. *resolve.Engine satisfies
// it; tests substitute a fake so the pin selection can be asserted without a
// registry.
type resolver interface {
	ResolvePinned(
		ctx context.Context, man *manifest.Manifest, pins resolve.Pins,
	) (*manifest.Lock, []string, error)
}

// engineFor builds the resolution engine the commands drive: the rocks adapter
// bound to the project's own tree, wrapped in a resolve.Engine anchored at the
// project directory so path dependencies resolve relative to the manifest.
func engineFor(opts Options) *resolve.Engine {
	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       filepath.Join(opts.ProjectDir, rocksDirName),
		WorkingDir: opts.ProjectDir,
		Servers:    opts.Servers,
		Logger:     opts.Logger,
	}))

	return resolve.NewEngine(adapter, opts.ProjectDir, opts.TtVersion)
}

// project is the on-disk state a run starts from.
type project struct {
	// manifest is the parsed and validated app.manifest.toml.
	manifest *manifest.Manifest
	// source is the exact bytes it was parsed from, which is what the editor
	// splices and what manifest_hash is taken over.
	source []byte
	// perm is the manifest file's current mode, carried so rewriting it does not
	// silently re-permission a file the user chmod'ed.
	perm os.FileMode
	// lock is the lock next to it, or nil when there is none. A project that has
	// never been built has no lock, which is a normal state.
	lock *manifest.Lock
}

// load reads the manifest and the lock from the project directory.
//
// A missing manifest is fatal: there is nothing to add to. A missing lock is
// not — it means the project has never resolved, so the run has nothing to pin
// and writes the first lock. A lock that exists but does not parse *is* fatal:
// treating it as absent would silently discard every pin it holds and move
// versions the user never asked to move.
func load(opts Options) (*project, error) {
	path := filepath.Join(opts.ProjectDir, manifestFileName)

	data, err := os.ReadFile(path) //nolint:gosec // Reads the caller's own manifest.
	if err != nil {
		return nil, stateErrorf("reading %s: %w", manifestFileName, err)
	}

	man, warnings, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, stateErrorf("parsing %s: %w", manifestFileName, err)
	}

	opts.emit(warnings)

	validationWarnings, err := man.Validate()
	if err != nil {
		return nil, stateErrorf("invalid manifest: %w", err)
	}

	opts.emit(validationWarnings)

	lock, err := loadLock(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	return &project{
		manifest: man,
		source:   data,
		perm:     manifestPerm(path),
		lock:     lock,
	}, nil
}

// loadLock parses the lock next to the manifest, reporting (nil, nil) when
// there is none.
func loadLock(projectDir string) (*manifest.Lock, error) {
	path := filepath.Join(projectDir, lockFileName)

	data, err := os.ReadFile(path) //nolint:gosec // Reads the caller's own lock.
	if os.IsNotExist(err) {
		return nil, nil //nolint:nilnil // A missing lock is a state, not a failure.
	}

	if err != nil {
		return nil, stateErrorf("reading %s: %w", lockFileName, err)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return nil, stateErrorf("parsing %s: %w", lockFileName, err)
	}

	return lock, nil
}

// manifestPerm reports the manifest's current mode, falling back to
// defaultFilePerm when it cannot be read.
func manifestPerm(path string) os.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return defaultFilePerm
	}

	return info.Mode().Perm()
}

// apply is the second half of every command: persist the edited manifest when
// there is one, re-resolve under pins, write the lock and report the moves.
//
// edited is nil for a run that changes no declaration (tt package update); then
// the manifest on disk is already the one to resolve.
//
// The re-parse in the middle is load-bearing and is the one thing in this
// package that is easy to get wrong. manifest_hash is SHA-256 over the raw
// source bytes a Manifest was parsed from, held in an unexported field, and it
// is what the lock records. Resolving the pre-edit *Manifest would therefore
// stamp the lock with the pre-edit hash, and resolve.IsStale would report the
// freshly written lock as stale on the very next command. Re-parsing the edited
// bytes is the only way to get a Manifest whose Hash is the one now on disk.
func apply(
	ctx context.Context, opts Options, res resolver,
	proj *project, edited []byte, pins resolve.Pins, change manifest.Change,
) (*Result, error) {
	man := proj.manifest

	if edited != nil {
		var err error

		man, err = writeAndReparse(opts, proj, edited)
		if err != nil {
			return nil, err
		}
	}

	lock, warnings, err := res.ResolvePinned(ctx, man, pins)
	if err != nil {
		if edited != nil {
			return nil, stateErrorf("resolving dependencies: %w; %w", err, ErrManifestEdited)
		}

		return nil, stateErrorf("resolving dependencies: %w", err)
	}

	opts.emit(warnings)

	err = writeLock(opts.ProjectDir, lock)
	if err != nil {
		return nil, err
	}

	return &Result{
		Manifest: man,
		Lock:     lock,
		Moves:    movesBetween(proj.lock, lock),
		Change:   change,
	}, nil
}

// writeAndReparse persists the edited manifest source and parses the bytes back
// into the Manifest the resolution runs against. See apply for why the re-parse
// cannot be skipped.
//
// Validation runs against the result, so an edit that produces a manifest the
// validator refuses is reported as such — with ErrManifestEdited, because by
// then the file on disk is the invalid one.
func writeAndReparse(opts Options, proj *project, edited []byte) (*manifest.Manifest, error) {
	path := filepath.Join(opts.ProjectDir, manifestFileName)

	//nolint:gosec // Rewrites the caller's own manifest, at the path it was read from.
	err := os.WriteFile(path, edited, proj.perm)
	if err != nil {
		return nil, stateErrorf("writing %s: %w", manifestFileName, err)
	}

	man, warnings, err := manifest.ParseManifest(edited)
	if err != nil {
		return nil, stateErrorf("parsing the edited %s: %w; %w",
			manifestFileName, err, ErrManifestEdited)
	}

	opts.emit(warnings)

	validationWarnings, err := man.Validate()
	if err != nil {
		return nil, stateErrorf("the edited manifest is invalid: %w; %w", err, ErrManifestEdited)
	}

	opts.emit(validationWarnings)

	return man, nil
}

// writeLock marshals lock and writes it next to the manifest.
func writeLock(projectDir string, lock *manifest.Lock) error {
	data, err := lock.Marshal()
	if err != nil {
		return stateErrorf("marshaling %s: %w", lockFileName, err)
	}

	err = os.WriteFile(filepath.Join(projectDir, lockFileName), data, defaultFilePerm)
	if err != nil {
		return stateErrorf("writing %s: %w", lockFileName, err)
	}

	return nil
}

// movesBetween diffs two locked closures into the moves the CLI reports.
func movesBetween(before, after *manifest.Lock) []Move {
	old := closureOf(before)
	current := closureOf(after)

	names := map[string]bool{}
	for name := range old {
		names[name] = true
	}

	for name := range current {
		names[name] = true
	}

	moves := make([]Move, 0, len(names))

	for _, name := range sortedKeys(names) {
		from, to := old[name], current[name]
		if from == to {
			continue
		}

		moves = append(moves, Move{Name: name, From: from, To: to})
	}

	return moves
}

// closureOf flattens a lock into rock name -> version across every product and
// the dev closure.
//
// Products hold independent closures, so two of them may legitimately pin
// different versions of one rock. The moves are a human report of what changed,
// not an input to anything, so a disagreement is settled by taking the first
// product in name order rather than by inventing a winner — the same rock
// reported once, deterministically.
func closureOf(lock *manifest.Lock) map[string]string {
	out := map[string]string{}
	if lock == nil {
		return out
	}

	for _, product := range sortedKeys(lock.Products) {
		for _, dependency := range lock.Products[product].Dependencies {
			if _, seen := out[dependency.Name]; !seen {
				out[dependency.Name] = dependency.Version
			}
		}
	}

	for _, dependency := range lock.DevDependencies {
		if _, seen := out[dependency.Name]; !seen {
			out[dependency.Name] = dependency.Version
		}
	}

	return out
}

// sortedKeys returns a map's keys in sorted order, so every report this package
// produces is deterministic.
func sortedKeys[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// declaredIn reports every place the manifest declares name: the two
// document-level tables and each component that names it. It backs the
// "is this rock declared at all" check the remove and targeted-update commands
// make, and its result is what their error message quotes.
func declaredIn(man *manifest.Manifest, name string) []string {
	var places []string

	if _, ok := man.Dependencies[name]; ok {
		places = append(places, "[dependencies]")
	}

	if _, ok := man.DevDependencies[name]; ok {
		places = append(places, "[dev_dependencies]")
	}

	for _, component := range sortedKeys(man.Components) {
		if _, ok := man.Components[component].Dependencies[name]; ok {
			places = append(places, fmt.Sprintf("[components.%s.dependencies]", component))
		}
	}

	return places
}
