package resolve

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/rocks"
	luarocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/deps"
)

// Dependency source values, mirroring the manifest package's closed enum.
const (
	manifestSourceRegistry = "registry"
	manifestSourcePath     = "path"
)

// errUnknownComponent guards against a product referencing a component the
// manifest does not define. Validation already rejects this; the guard keeps
// the engine from panicking on an unvalidated manifest.
var errUnknownComponent = errors.New("product references unknown component")

// providedRocks are dependency names supplied by the Tarantool VM itself, so
// they are never resolved against a registry. Mirrors lib/luarocks/deps'
// unexported set (Tarantool embeds LuaJIT 5.1); kept in sync deliberately.
//
//nolint:gochecknoglobals // provided-rock set, effectively a constant.
var providedRocks = map[string]bool{
	"lua":      true,
	"luajit":   true,
	"luabitop": true,
}

// depReq is one requirement to resolve: a name, the constraint in both string
// (for the adapter) and parsed (for compatibility checks) form, and the source
// it comes from. Direct requirements may carry a registry override or a path;
// transitive requirements never do.
type depReq struct {
	name           string
	constraintExpr string
	constraints    []luarocks.VersionConstraint
	registry       string
	source         string
	path           string
	// multiDeclared marks a direct dependency declared in more than one place
	// (global plus a component, or two components), so a no-match resolves to a
	// declaration conflict rather than a plain "no version" error.
	multiDeclared bool
}

// resolvedDep is a chosen dependency: its lock entry plus the parsed version,
// kept so later branches can check their constraints against the pick.
type resolvedDep struct {
	lockDep manifest.LockDependency
	version luarocks.Version
}

// walker performs the greedy depth-first closure walk. It mirrors
// lib/luarocks/deps.Resolve (newest-that-fits, deepest-first topo order, one
// version per name, cycle and conflict detection with the offending chain) but
// drives the adapter per chosen rock - one rockspec fetch each, honoring
// per-dependency registry overrides - instead of preloading every candidate.
type walker struct {
	engine   *Engine
	chosen   map[string]*resolvedDep
	inFlight map[string]bool
	order    []string // post-order: deepest dependency first
	warnings []string
}

// walk visits each requirement in reqs, recursing into a chosen rock's
// transitive dependencies before moving on to its siblings. parent and chain
// are carried for diagnostics.
func (w *walker) walk(ctx context.Context, parent string, reqs []depReq, chain []string) error {
	for _, req := range reqs {
		ctxErr := ctx.Err()
		if ctxErr != nil {
			return fmt.Errorf("resolving dependencies: %w", ctxErr)
		}

		if providedRocks[req.name] {
			continue
		}

		if w.inFlight[req.name] {
			return &cycleError{name: req.name, chain: appendChain(chain, req.name)}
		}

		existing, chosen := w.chosen[req.name]
		if chosen {
			if !deps.Match(existing.version, req.constraints) {
				return &conflictError{
					detail: fmt.Sprintf("chose %s %s but %s requires %s",
						req.name, existing.version.Raw, parentLabel(parent), constraintLabel(req)),
					chain: appendChain(chain, req.name),
				}
			}

			continue
		}

		w.inFlight[req.name] = true

		resolved, children, err := w.resolveOne(ctx, req)
		if err != nil {
			return err
		}

		w.chosen[req.name] = resolved

		walkErr := w.walk(ctx, req.name, children, append(chain, req.name))
		if walkErr != nil {
			return walkErr
		}

		delete(w.inFlight, req.name)

		w.order = append(w.order, req.name)
	}

	return nil
}

// resolveOne resolves a single requirement into its lock entry and the
// transitive requirements it introduces.
func (w *walker) resolveOne(ctx context.Context, req depReq) (*resolvedDep, []depReq, error) {
	if req.source == manifestSourcePath {
		return w.engine.resolvePath(req)
	}

	resolved, err := w.engine.adapter.Resolve(ctx, req.name, req.constraintExpr, req.registry)
	if err != nil {
		// A multiply-declared direct dependency with no satisfying version is a
		// conflict between its declarations, not a plain missing version.
		if req.multiDeclared && errors.Is(err, rocks.ErrNoMatch) {
			return nil, nil, &conflictError{
				detail: fmt.Sprintf(
					"global and per-component declarations of %q require incompatible versions",
					req.name),
				chain: nil,
			}
		}

		return nil, nil, fmt.Errorf("resolving %q: %w", req.name, err)
	}

	spec, err := w.engine.adapter.Metadata(ctx, resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("metadata for %q: %w", req.name, err)
	}

	checksum, ok := rocks.Checksum(spec)
	if !ok {
		w.warnings = append(w.warnings, fmt.Sprintf(
			"registry gave no md5 for %s %s; reproducibility is not guaranteed",
			resolved.Name, resolved.Version.Raw))
	}

	resolvedDependency := &resolvedDep{
		lockDep: manifest.LockDependency{
			Name:        resolved.Name,
			Version:     resolved.Version.Raw,
			Source:      manifestSourceRegistry,
			Checksum:    checksum,
			Path:        "",
			ContentHash: "",
		},
		version: resolved.Version,
	}

	return resolvedDependency, transitiveReqs(spec.Dependencies), nil
}

// transitiveReqs turns a rockspec's dependency list into requirements. They
// carry no registry override and no path - transitive rocks come from the
// default server list.
func transitiveReqs(rockDeps []luarocks.Dep) []depReq {
	out := make([]depReq, 0, len(rockDeps))

	for _, dependency := range rockDeps {
		out = append(out, depReq{
			name:           dependency.Name,
			constraintExpr: formatConstraints(dependency.Constraints),
			constraints:    dependency.Constraints,
			registry:       "",
			source:         manifestSourceRegistry,
			path:           "",
			multiDeclared:  false,
		})
	}

	return out
}

// effectiveDeps assembles a product's direct dependencies: the union of the
// global [dependencies] and the per-component [components.<c>.dependencies]
// over every component of the product. A dependency declared in more than one
// place is merged - its version constraints are AND'd together - but the
// declarations must agree on source, path and registry; a disagreement is a
// conflict. The result is sorted by name for a deterministic closure.
func effectiveDeps(man *manifest.Manifest, product manifest.Product) ([]depReq, error) {
	byName := map[string]*depReq{}

	globalErr := mergeDeps(byName, "dependencies", man.Dependencies)
	if globalErr != nil {
		return nil, globalErr
	}

	for _, name := range product.Components {
		component, defined := man.Components[name]
		if !defined {
			return nil, fmt.Errorf("%w: %q", errUnknownComponent, name)
		}

		field := "components." + name + ".dependencies"

		compErr := mergeDeps(byName, field, component.Dependencies)
		if compErr != nil {
			return nil, compErr
		}
	}

	out := make([]depReq, 0, len(byName))

	for _, name := range sortedKeys(byName) {
		req := byName[name]

		constraints, parseErr := deps.ParseConstraints(req.constraintExpr)
		if parseErr != nil {
			return nil, fmt.Errorf("dependency %q: %w", name, parseErr)
		}

		req.constraints = constraints
		out = append(out, *req)
	}

	return out, nil
}

// mergeDeps folds one dependency map into the accumulator, merging repeats and
// rejecting conflicting declarations.
func mergeDeps(
	byName map[string]*depReq, field string, declared map[string]manifest.Dependency,
) error {
	for _, name := range sortedKeys(declared) {
		dependency := declared[name]

		existing, seen := byName[name]
		if !seen {
			byName[name] = &depReq{
				name:           name,
				constraintExpr: dependency.Version,
				constraints:    nil,
				registry:       dependency.Registry,
				source:         dependency.Source,
				path:           dependency.Path,
				multiDeclared:  false,
			}

			continue
		}

		mergeErr := mergeInto(existing, name, dependency)
		if mergeErr != nil {
			return fmt.Errorf("%s.%s: %w", field, name, mergeErr)
		}
	}

	return nil
}

// mergeInto folds a repeated declaration of name into existing.
func mergeInto(existing *depReq, name string, dependency manifest.Dependency) error {
	existing.multiDeclared = true

	if existing.source != dependency.Source {
		return &conflictError{
			detail: fmt.Sprintf("%q is declared as source %q and %q",
				name, existing.source, dependency.Source),
			chain: nil,
		}
	}

	if dependency.Source == manifestSourcePath {
		if existing.path != dependency.Path {
			return &conflictError{
				detail: fmt.Sprintf("%q path %q and %q disagree",
					name, existing.path, dependency.Path),
				chain: nil,
			}
		}

		return nil
	}

	if dependency.Registry != "" {
		if existing.registry != "" && existing.registry != dependency.Registry {
			return &conflictError{
				detail: fmt.Sprintf("%q registry %q and %q disagree",
					name, existing.registry, dependency.Registry),
				chain: nil,
			}
		}

		existing.registry = dependency.Registry
	}

	existing.constraintExpr = joinConstraints(existing.constraintExpr, dependency.Version)

	return nil
}

// joinConstraints AND's two constraint expressions by comma-joining their
// non-empty parts, matching deps.ParseConstraints' grammar.
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

// formatConstraints renders a parsed constraint list back to the comma-joined
// string form the adapter re-parses. An empty list yields "" (any version).
func formatConstraints(constraints []luarocks.VersionConstraint) string {
	parts := make([]string, 0, len(constraints))
	for _, constraint := range constraints {
		parts = append(parts, constraint.Op+constraint.Version.Raw)
	}

	return strings.Join(parts, ",")
}

// appendChain returns chain with name appended, without aliasing chain's array.
func appendChain(chain []string, name string) []string {
	return append(append([]string{}, chain...), name)
}

// parentLabel names the requiring rock in a conflict message, or "the product"
// for a direct dependency.
func parentLabel(parent string) string {
	if parent == "" {
		return "the product"
	}

	return parent
}

// constraintLabel renders a requirement's constraint for a conflict message.
func constraintLabel(req depReq) string {
	if req.constraintExpr == "" {
		return "any version"
	}

	return req.constraintExpr
}

func sortedKeys[V any](collection map[string]V) []string {
	keys := make([]string, 0, len(collection))
	for key := range collection {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}
