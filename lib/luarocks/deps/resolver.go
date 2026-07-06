package deps

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// providedRocks are dependency names that ship inside the Tarantool VM and
// therefore never need to be fetched. Matches upstream
// `util.get_rocks_provided` minimum set; only `lua` is universally
// pre-supplied for our purposes (Tarantool embeds LuaJIT 5.1).
var providedRocks = map[string]bool{
	"lua":      true,
	"luajit":   true,
	"luabitop": true,
}

// IsProvided reports whether name is a rock the Tarantool VM supplies itself,
// so callers building their own resolution over the same registry can skip it
// exactly as Resolve does, without duplicating the set.
func IsProvided(name string) bool {
	return providedRocks[name]
}

// Resolve performs a greedy depth-first walk over root.Dependencies,
// choosing the newest version of each dep that satisfies its constraints
// and recursing into the chosen rock's transitive dependencies before
// moving on to its siblings.
//
// The result is in topological order (deepest-dep first), suitable for
// passing to Rocks.Install in sequence. The root rock itself is NOT
// included in the result — callers install it separately after their
// chosen pre-requisites are in place.
//
// Conflicts (two branches demanding incompatible versions of the same
// dep) and cycles are reported as errors with the dependency chain
// included in the message.
//
// `lua` and other VM-provided names short-circuit: they are silently
// dropped from the install list.
func Resolve(
	ctx context.Context, root *rocks.Rockspec, idx rocks.RemoteIndex, opts ...Option,
) ([]rocks.InstallStep, error) {
	if root == nil {
		return nil, errors.New("deps.Resolve: nil root rockspec")
	}

	r := &resolver{
		idx:      idx,
		chosen:   map[string]*rocks.InstallStep{},
		inFlight: map[string]bool{},
	}

	for _, opt := range opts {
		opt(r)
	}

	if err := r.walk(ctx, root.Package, root.Dependencies, nil); err != nil {
		return nil, err
	}
	// Topo order: by recording when each name leaves the recursion (post-
	// order), the resulting slice already has deepest deps first. r.order
	// preserves that order.
	out := make([]rocks.InstallStep, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, *r.chosen[name])
	}

	return out, nil
}

// SpecFetcher supplies a picked rock's rockspec on demand. See WithSpecFetcher.
type SpecFetcher func(ctx context.Context, rock rocks.VersionedRock) (*rocks.Rockspec, error)

// Option configures Resolve.
type Option func(*resolver)

// WithSpecFetcher makes Resolve fetch a chosen rock's rockspec on demand when
// the index did not preload it. After pickNewest selects a version, if that
// version has no Spec, Resolve calls fetch on the picked rock only (never on
// the rejected candidates) and continues the transitive walk with the result.
//
// This is what lets a non-preloading index (e.g. remote.HTTPRemoteIndex, which
// returns bare name/version/URL rows) drive a full transitive resolution
// without fetching every candidate's rockspec: one fetch per chosen rock. A
// fetch error aborts the resolution. Without this option Resolve keeps its
// preload-only behavior — it recurses only into versions whose Spec the index
// already populated.
func WithSpecFetcher(fetch SpecFetcher) Option {
	return func(r *resolver) { r.fetch = fetch }
}

type resolver struct {
	idx      rocks.RemoteIndex
	fetch    SpecFetcher
	chosen   map[string]*rocks.InstallStep
	inFlight map[string]bool
	order    []string // post-order — deepest first
}

// walk visits every entry in deps. parent is included in cycle errors.
// chain tracks the dep-name stack for diagnostics.
func (r *resolver) walk(ctx context.Context, parent string, deps []rocks.Dep, chain []string) error {
	for _, d := range deps {
		if err := ctx.Err(); err != nil {
			return err
		}

		if providedRocks[d.Name] {
			continue
		}

		if r.inFlight[d.Name] {
			return fmt.Errorf("deps.Resolve: cycle detected on %q in chain %v", d.Name, append(chain, d.Name))
		}

		if existing, ok := r.chosen[d.Name]; ok {
			// Already chose a version for this name. Make sure the version
			// also satisfies the current constraints; if not, that's a
			// hard conflict.
			if !Match(existing.Version, d.Constraints) {
				return fmt.Errorf(
					"deps.Resolve: conflict for %q: previously chose %s but %s requires %v",
					d.Name, existing.Version.Raw, parent, formatConstraints(d.Constraints))
			}

			continue
		}

		r.inFlight[d.Name] = true

		candidates, err := r.idx.Query(ctx, d.Name)
		if err != nil {
			return fmt.Errorf("deps.Resolve: query %q: %w", d.Name, err)
		}

		picked, ok := pickNewest(candidates, d.Constraints)
		if !ok {
			return fmt.Errorf(
				"deps.Resolve: no version of %q satisfies %s (parent=%s, %d candidates)",
				d.Name, formatConstraints(d.Constraints), parent, len(candidates))
		}
		// Preload the picked rock's rockspec on demand when the index did not
		// carry one, so the transitive walk below can proceed. Only the chosen
		// version is fetched — never the rejected candidates.
		if picked.Spec == nil && r.fetch != nil {
			spec, err := r.fetch(ctx, picked)
			if err != nil {
				return fmt.Errorf("deps.Resolve: fetch rockspec for %q: %w", d.Name, err)
			}

			picked.Spec = spec
		}

		step := &rocks.InstallStep{
			Name:     picked.Name,
			Version:  picked.Version,
			URL:      picked.URL,
			Rockspec: picked.Spec,
		}
		r.chosen[d.Name] = step

		// Recurse into the picked version's deps. With a spec fetcher the
		// picked version now carries its rockspec; without one we only have
		// transitive info when the index preloaded a *Rockspec (the
		// facade does this; manifest-based indices typically don't preload).
		if picked.Spec != nil {
			child := append(append([]string{}, chain...), d.Name)
			if err := r.walk(ctx, d.Name, picked.Spec.Dependencies, child); err != nil {
				return err
			}
		}

		delete(r.inFlight, d.Name)
		r.order = append(r.order, d.Name)
	}

	return nil
}

// pickNewest selects the highest version in candidates whose Version
// matches all of cs.
func pickNewest(candidates []rocks.VersionedRock, cs []rocks.VersionConstraint) (rocks.VersionedRock, bool) {
	filtered := make([]rocks.VersionedRock, 0, len(candidates))

	for _, c := range candidates {
		if Match(c.Version, cs) {
			filtered = append(filtered, c)
		}
	}

	if len(filtered) == 0 {
		return rocks.VersionedRock{}, false
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		return Compare(filtered[i].Version, filtered[j].Version) > 0
	})

	return filtered[0], true
}

func formatConstraints(cs []rocks.VersionConstraint) string {
	if len(cs) == 0 {
		return "(any)"
	}

	var out strings.Builder

	for i, c := range cs {
		if i > 0 {
			out.WriteString(", ")
		}

		out.WriteString(c.Op + " " + c.Version.Raw)
	}

	return out.String()
}
