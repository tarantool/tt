package deps

import (
	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
)

// LockState says what the lock next to the manifest is worth as an answer to
// "which version is this dependency at".
type LockState string

const (
	// LockCurrent is a lock that still reflects the manifest: every version it
	// records is the one a build would use.
	LockCurrent LockState = "current"
	// LockStale is a lock the manifest has moved away from. Its versions are
	// what the last resolution chose, not what the next one will, so a report
	// over it says so rather than presenting them as current.
	LockStale LockState = "stale"
	// LockMissing is a project that has never resolved. Only the declarations
	// are known then; nothing carries a version yet.
	LockMissing LockState = "missing"
)

// Entry is one dependency in a report: what the manifest declares, and what the
// lock resolved it to.
//
// A direct entry is one the manifest declares — globally or through a component
// of the product — and it carries the constraint that was declared. An indirect
// one is in the closure because something else required it; it has a version
// and no constraint, because no declaration in this manifest asked for it.
type Entry struct {
	// Name is the rock name.
	Name string `json:"name" yaml:"name"`
	// Constraint is the declared version constraint, comma-joined when the name
	// is declared in more than one place — the same AND the resolver applies, so
	// the report shows what actually constrains the pick. Empty for an indirect
	// dependency.
	Constraint string `json:"constraint,omitempty" yaml:"constraint,omitempty"`
	// Version is the version the lock records, empty when no lock does.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Source is "registry" or "path", from the declaration when there is one and
	// from the lock otherwise.
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
	// Path is the directory a path dependency points at, relative to the
	// manifest.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Registry is the per-dependency server override, when one is declared.
	Registry string `json:"registry,omitempty" yaml:"registry,omitempty"`
	// Direct marks a dependency the manifest declares itself.
	Direct bool `json:"direct" yaml:"direct"`
	// DeclaredIn names the manifest tables that declare it, in the order
	// declaredIn reports them. Empty for an indirect dependency.
	DeclaredIn []string `json:"declared_in,omitempty" yaml:"declared_in,omitempty"`
}

// ProductEntries is one product's dependencies: what it declares, then what its
// closure pulled in behind those declarations.
type ProductEntries struct {
	// Name is the product name. A manifest that declares no products at all
	// reports one group with an empty name — its [dependencies] are declared and
	// worth showing even though nothing resolves them into a closure yet.
	Name string `json:"name" yaml:"name"`
	// Dependencies are the product's dependencies: direct ones first, then
	// indirect ones, each group in name order.
	Dependencies []Entry `json:"dependencies" yaml:"dependencies"`
}

// Report is the answer tt package deps prints: every dependency of the current
// manifest with the version the lock resolved it to.
type Report struct {
	// Package is the manifest's package name.
	Package string `json:"package" yaml:"package"`
	// Lock says how far the lock's versions can be trusted.
	Lock LockState `json:"lock" yaml:"lock"`
	// LockReason explains a stale lock, in the resolver's own words. Empty
	// otherwise.
	LockReason string `json:"lock_reason,omitempty" yaml:"lock_reason,omitempty"`
	// Products are the runtime dependencies, one group per product.
	Products []ProductEntries `json:"products" yaml:"products"`
	// DevDependencies are the dev closure, which the manifest declares globally
	// rather than per product.
	DevDependencies []Entry `json:"dev_dependencies,omitempty" yaml:"dev_dependencies,omitempty"`
}

// declaration is one dependency as the manifest declares it, merged across
// every table that declares it.
type declaration struct {
	constraint string
	source     string
	path       string
	registry   string
	places     []string
}

// Deps reports what the current manifest depends on, at the versions the lock
// resolved.
//
// It reads the manifest and the lock and nothing else: no registry is queried,
// nothing is fetched and the lock is not rewritten. That is the whole point of
// the command — it answers "what does this project depend on" in the time it
// takes to read two files, and a project whose lock is stale gets that fact
// reported (Report.Lock) rather than a silently re-resolved answer that the
// files on disk do not agree with.
//
// It is deliberately not tt package list: this is what the manifest declares,
// whether or not any of it was ever installed.
func Deps(opts Options) (*Report, error) {
	proj, err := load(opts)
	if err != nil {
		return nil, err
	}

	state, reason, err := lockState(opts.ProjectDir, proj)
	if err != nil {
		return nil, err
	}

	report := &Report{
		Package:         proj.manifest.Package.Name,
		Lock:            state,
		LockReason:      reason,
		Products:        productEntries(proj),
		DevDependencies: devEntries(proj),
	}

	return report, nil
}

// lockState classifies the lock the report is read against.
//
// Staleness is asked of the resolver rather than re-implemented here, so the
// command cannot drift from what a build would decide. It needs no adapter: the
// check reads the manifest hash and the path dependencies' contents, both of
// which are on disk.
func lockState(projectDir string, proj *project) (LockState, string, error) {
	if proj.lock == nil {
		return LockMissing, "", nil
	}

	stale, reason, err := resolve.IsStale(projectDir, proj.manifest, proj.lock)
	if err != nil {
		return "", "", stateErrorf("checking whether the lock is up to date: %w", err)
	}

	if stale {
		return LockStale, reason, nil
	}

	return LockCurrent, "", nil
}

// productEntries builds one group per product, merging what the product
// declares with what its locked closure holds.
//
// A manifest declaring no products still reports its [dependencies]: they are
// declared, the user asked what this project depends on, and answering
// "nothing" over a populated [dependencies] table would be wrong. The group is
// then unnamed, which is what it is — no product owns them yet.
func productEntries(proj *project) []ProductEntries {
	man := proj.manifest

	names := sortedKeys(man.Products)
	if len(names) == 0 {
		names = []string{""}
	}

	out := make([]ProductEntries, 0, len(names))

	for _, name := range names {
		declared := declaredFor(man, name)

		var locked []manifest.LockDependency
		if proj.lock != nil {
			locked = proj.lock.Products[name].Dependencies
		}

		out = append(out, ProductEntries{
			Name:         name,
			Dependencies: entriesOf(declared, locked),
		})
	}

	return out
}

// devEntries builds the dev group: [dev_dependencies] against the lock's dev
// closure, which is global rather than per product.
func devEntries(proj *project) []Entry {
	man := proj.manifest
	if len(man.DevDependencies) == 0 {
		return nil
	}

	declared := map[string]*declaration{}
	mergeDeclarations(declared, "[dev_dependencies]", man.DevDependencies)

	var locked []manifest.LockDependency
	if proj.lock != nil {
		locked = proj.lock.DevDependencies
	}

	return entriesOf(declared, locked)
}

// declaredFor collects the declarations that apply to one product: the
// document-level [dependencies] plus the tables of the components the product
// is built from. It mirrors the resolver's effectiveDeps — same tables, same
// comma-joined merge — without reaching for the resolver's unexported types.
//
// An empty product name is the no-products case: only the global table applies.
func declaredFor(man *manifest.Manifest, product string) map[string]*declaration {
	out := map[string]*declaration{}

	mergeDeclarations(out, "[dependencies]", man.Dependencies)

	if product == "" {
		return out
	}

	for _, name := range man.Products[product].Components {
		component, defined := man.Components[name]
		if !defined {
			// Validation refuses this; a report over an unvalidated manifest
			// simply has nothing to add for the missing component.
			continue
		}

		mergeDeclarations(out, "[components."+name+".dependencies]", component.Dependencies)
	}

	return out
}

// mergeDeclarations folds one declaration table into the accumulator, recording
// every place a name is declared and AND-ing repeated constraints the way the
// resolver does.
func mergeDeclarations(
	out map[string]*declaration, place string, declared map[string]manifest.Dependency,
) {
	for _, name := range sortedKeys(declared) {
		dependency := declared[name]

		existing, seen := out[name]
		if !seen {
			out[name] = &declaration{
				constraint: dependency.Version,
				source:     dependency.Source,
				path:       dependency.Path,
				registry:   dependency.Registry,
				places:     []string{place},
			}

			continue
		}

		existing.constraint = joinConstraints(existing.constraint, dependency.Version)
		existing.places = append(existing.places, place)

		if existing.registry == "" {
			existing.registry = dependency.Registry
		}
	}
}

// joinConstraints AND's two constraint expressions the way the resolver's own
// merge does: comma-joined, skipping empty halves.
func joinConstraints(left, right string) string {
	switch {
	case left == "":
		return right
	case right == "":
		return left
	default:
		return left + "," + right
	}
}

// entriesOf turns one closure into report entries: the declared dependencies
// first, in name order, then whatever the closure holds that no declaration
// asked for.
//
// A declared dependency the closure does not hold is still reported, without a
// version. That is the ordinary state of a project that has not resolved since
// the declaration was added, and dropping it would answer "you do not depend on
// it" over a line the user just wrote.
func entriesOf(declared map[string]*declaration, locked []manifest.LockDependency) []Entry {
	versions := make(map[string]manifest.LockDependency, len(locked))
	for _, dependency := range locked {
		versions[dependency.Name] = dependency
	}

	direct := make([]Entry, 0, len(declared))

	for _, name := range sortedKeys(declared) {
		decl := declared[name]
		pick := versions[name]

		direct = append(direct, Entry{
			Name:       name,
			Constraint: decl.constraint,
			Version:    pick.Version,
			Source:     orElse(decl.source, pick.Source),
			Path:       orElse(decl.path, pick.Path),
			Registry:   decl.registry,
			Direct:     true,
			DeclaredIn: decl.places,
		})
	}

	indirect := make([]Entry, 0, len(locked))

	for _, name := range sortedKeys(versions) {
		if _, isDirect := declared[name]; isDirect {
			continue
		}

		pick := versions[name]

		indirect = append(indirect, Entry{
			Name:       name,
			Constraint: "",
			Version:    pick.Version,
			Source:     pick.Source,
			Path:       pick.Path,
			Registry:   "",
			Direct:     false,
			DeclaredIn: nil,
		})
	}

	return append(direct, indirect...)
}

// orElse prefers the declaration's own value and falls back to the lock's, so
// an indirect-only field (the lock's source for a rock nothing declares) still
// reaches the report.
func orElse(declared, locked string) string {
	if declared != "" {
		return declared
	}

	return locked
}
