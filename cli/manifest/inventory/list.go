// Package inventory implements the two commands that work over what an install
// left on disk: tt package list, which reports the packages present in a scope,
// and tt package uninstall, which removes a guest package and the shared
// dependencies it was the last to hold, or, in the user and system scopes, an
// application with all its installed versions.
//
// Both are pure metadata operations. Nothing here contacts a registry, runs a
// build, or re-resolves anything: the source of truth is what install wrote and
// cli/manifest/state reads back — manifests/<pkg>/ in a project, the versions/
// and apps/ directories of the user and system roots.
//
// List reports what is on disk, which is deliberately not what tt package deps
// reports — deps prints the dependencies the current manifest declares, whether
// or not they were ever installed.
package inventory

import (
	"sort"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// ListOptions selects what to list.
type ListOptions struct {
	// ProjectDir is the install-root for project scope (the caller's cwd). It
	// must be absolute; user and system scopes derive their roots from the
	// environment and ignore it.
	ProjectDir string
	// Scope selects which tree to read. The zero value ("") is project.
	Scope state.Scope
	// Rocks asks for the rock index alongside the packages: every rock in the
	// project with the packages that brought it and what it requires in turn;
	// the user and system scopes refuse it, their versions sharing no tree. It is
	// the question a plain listing cannot answer — which of a package's rocks
	// would go with it, and which another package still holds — so it is computed
	// only when asked for.
	Rocks bool
}

// Entry is one installed package in a listing.
type Entry struct {
	// Name is the package name, taken from its manifest.
	Name string `json:"name" yaml:"name"`
	// Version is the recorded version. Empty when nothing recorded one: a guest
	// installed by a tt that predates the VERSION metadata, or a project with no
	// VERSION pin next to its manifest. For an application of the user or
	// system scope it is the active version, empty when none is active.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Versions are the installed versions of an application of the user or
	// system scope, lowest first. Empty in the project scope, where a package is
	// installed at one version.
	Versions []string `json:"versions,omitempty" yaml:"versions,omitempty"`
	// Primary marks the project's own package rather than a guest installed from
	// an archive. Only the project scope can hold one, and uninstall refuses to
	// touch it.
	Primary bool `json:"primary" yaml:"primary"`
	// Description is the manifest's package description, when it declares one.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Dependencies are the versions this package's lock pins, keyed by rock name.
	// This is the whole transitive closure, not just what the manifest asked for:
	// a package owns every rock it caused to be installed, at whatever depth, and
	// that overlap is what uninstall counts over.
	Dependencies map[string]string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	// Direct are the subset of Dependencies the manifest declares itself, in name
	// order; the rest of the closure arrived behind them. Which rock pulled an
	// indirect one in comes from RockEntry.Requires, not from the lock,
	// which stores the closure flattened.
	Direct []string `json:"direct,omitempty" yaml:"direct,omitempty"`
}

// RockEntry is one rock present in the scope, with every package that brought
// it. It is the reverse of Entry.Dependencies: the same ownership relation read
// from the rock's side, which is the side that answers what an uninstall would
// take with it.
type RockEntry struct {
	// Name is the rock name.
	Name string `json:"name" yaml:"name"`
	// Version is the version in the tree, falling back to what a lock pinned when
	// the rock has no files (a lock recording a dependency that was never laid
	// down).
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Owners are the packages whose lock pins it, in name order.
	Owners []string `json:"owners" yaml:"owners"`
	// Shared marks a dependency more than one package brought. It is the question
	// the view exists to answer, so it is stated rather than left to be counted.
	Shared bool `json:"shared" yaml:"shared"`
	// Installed marks a dependency that is also an installed package in its own
	// right. Those are never removed as collateral — only an uninstall naming
	// them removes them — so the distinction matters when reading the tree.
	Installed bool `json:"installed,omitempty" yaml:"installed,omitempty"`
	// Requires are the rocks this one pulls in, in name order, read from the
	// tree manifest LuaRocks maintains. Empty when the tree records no edges for
	// it — either the rock needs nothing, or the tree carries no manifest at all.
	Requires []string `json:"requires,omitempty" yaml:"requires,omitempty"`
}

// Listing is the result of tt package list.
type Listing struct {
	// Scope is the tree that was read.
	Scope state.Scope `json:"scope" yaml:"scope"`
	// Root is the install-root of that scope, so a machine-readable listing says
	// which tree it describes.
	Root string `json:"root" yaml:"root"`
	// Packages are the packages present, primary first and guests in name order.
	Packages []Entry `json:"packages" yaml:"packages"`
	// Rocks is the reverse ownership index, in name order. Nil unless
	// ListOptions.Rocks asked for it.
	//
	// It is deliberately not called "dependencies": a package entry already has a
	// "dependencies" key holding a name→version object, and reusing the name for
	// an array of records would put two shapes behind one word.
	Rocks []RockEntry `json:"rocks,omitempty" yaml:"rocks,omitempty"`
}

// List reports the packages installed in the selected scope. A scope with
// nothing installed is an empty listing, not an error: a project that has never
// been installed into is a normal state, not a broken one.
func List(opts ListOptions) (*Listing, error) {
	scope, err := parseScope(opts.Scope)
	if err != nil {
		return nil, err
	}

	if scope != state.ScopeProject {
		return listApps(scope, opts)
	}

	lay := state.ProjectLayout(opts.ProjectDir)

	packages, err := state.Packages(lay)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	listing := &Listing{
		Scope:    scope,
		Root:     lay.Root,
		Packages: make([]Entry, 0, len(packages)),
		// Nil unless asked for. The renderer reads nil as "no rock view was
		// requested" and falls back to the flat table, so an empty-but-non-nil
		// index — a scope that was asked for the view and holds nothing — still
		// renders as the tree.
		Rocks: nil,
	}

	for _, pkg := range packages {
		listing.Packages = append(listing.Packages, entryOf(pkg))
	}

	if opts.Rocks {
		listing.Rocks = rockIndex(lay, packages)
	}

	return listing, nil
}

// rockIndex inverts the per-package pins into one entry per rock, carrying the
// packages that brought it.
//
// The version reported is the one in the tree rather than any single lock's pin:
// packages sharing a rock hold one copy between them, and a later install can
// reconcile it to a version an earlier package's lock never named. What is on
// disk is what an uninstall would remove, so that is what the view shows.
func rockIndex(lay state.Layout, packages []state.Package) []RockEntry {
	owners := map[string][]string{}
	pins := map[string]string{}
	edges := state.DependencyEdges(lay)

	for _, pkg := range packages {
		for name, version := range state.LockedDependencies(pkg.Lock) {
			owners[name] = append(owners[name], pkg.Name)

			if _, seen := pins[name]; !seen {
				pins[name] = version
			}
		}
	}

	entries := make([]RockEntry, 0, len(owners))

	for _, name := range sortedKeys(owners) {
		holders := owners[name]
		sort.Strings(holders)

		version, onDisk := state.InstalledVersion(lay, name)
		if !onDisk {
			version = pins[name]
		}

		_, installed := state.Find(packages, name)

		entries = append(entries, RockEntry{
			Name:      name,
			Version:   version,
			Owners:    holders,
			Shared:    len(holders) > 1,
			Installed: installed,
			Requires:  edges[name],
		})
	}

	return entries
}

// sortedKeys returns a map's keys in sorted order, so the index is
// deterministic.
func sortedKeys[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}

// entryOf projects an installed package onto its listing entry.
func entryOf(pkg state.Package) Entry {
	entry := Entry{
		Name:         pkg.Name,
		Version:      pkg.Version,
		Versions:     nil,
		Primary:      pkg.Primary,
		Description:  "",
		Dependencies: state.LockedDependencies(pkg.Lock),
		Direct:       nil,
	}

	if pkg.Manifest != nil {
		entry.Description = pkg.Manifest.Package.Description
	}

	// A manifest can declare a dependency the lock does not pin (a dev-only or
	// unresolved one), so intersect rather than taking the declarations whole:
	// Direct must stay a subset of what is actually installed.
	declared := state.DeclaredDependencies(pkg.Manifest)

	for _, name := range sortedKeys(entry.Dependencies) {
		if _, ok := declared[name]; ok {
			entry.Direct = append(entry.Direct, name)
		}
	}

	// An empty map would render as an explicit "dependencies: {}" in the machine
	// output; nil omits the key entirely, which reads as "brought nothing".
	if len(entry.Dependencies) == 0 {
		entry.Dependencies = nil
	}

	return entry
}

// parseScope validates the scope, mapping a bad value to exit 1.
func parseScope(raw state.Scope) (state.Scope, error) {
	scope, err := state.ParseScope(string(raw))
	if err != nil {
		return "", stateErrorf("%w", err)
	}

	return scope, nil
}
