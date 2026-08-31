// Package resolve is the dependency-resolution engine for tt packages: it turns
// the declared constraints of a manifest into a pinned closure and builds the
// lock (app.manifest.lock) - declared, resolved, locked.
//
// Resolution runs per product: each product gets its own closure over the
// newest versions that satisfy every constraint. [dev_dependencies] adds one
// further closure, global rather than per-product, resolved last and against
// the products' own picks - it lands in the same .rocks/ tree, which can hold
// only one version of a rock. A caller that must not move versions it already
// holds - every command other than tt package update - passes a pin set (see
// Pins) so the lock's own picks are preferred over the
// newest ones. The engine takes an already parsed manifest plus an adapter over
// go-luarocks; it never touches the network itself. The adapter
// (cli/manifest/rocks) queries registries, fetches rockspecs and reports source
// checksums; the policy - which versions, by which product, what lands in the
// lock - lives here.
//
// Materializing .rocks/ from a lock, editing the manifest on add/remove and
// bundling runtimes into the lock are other packages' work. The tt package
// resolve/update/deps commands are thin wrappers over this engine.
package resolve

import (
	"context"
	"errors"
	"fmt"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/rocks"
)

// Adapter is the slice of cli/manifest/rocks the engine drives. The engine
// depends on this interface rather than the concrete *rocks.Adapter so tests
// can substitute a fake registry without a live server; *rocks.Adapter
// satisfies it directly.
type Adapter interface {
	// Resolve picks the newest version of name satisfying constraintExpr,
	// querying servers in order (first-found-wins) or the single registry when
	// non-empty. It returns rocks.ErrNotFound when no server has the rock and
	// rocks.ErrNoMatch when none of its versions satisfy the constraints.
	Resolve(ctx context.Context, name, constraintExpr, registry string) (rocks.ResolvedRock, error)

	// Metadata fetches and evaluates a resolved rock's rockspec, with the
	// runtime platforms merged in, so its transitive dependencies and
	// source.md5 are visible.
	Metadata(ctx context.Context, rock rocks.ResolvedRock) (*luarocks.Rockspec, error)

	// LocalMetadata evaluates the rockspec of a path dependency's directory, or
	// returns (nil, nil) when the directory ships no rockspec (a leaf path
	// dependency).
	LocalMetadata(dir string) (*luarocks.Rockspec, error)
}

// Engine resolves a manifest into a lock over an Adapter.
type Engine struct {
	adapter Adapter

	// projectDir is the directory the manifest lives in; path dependencies are
	// resolved relative to it and their content hashes are read from it.
	projectDir string

	// generatedBy is the "tt <version>" string stamped into the lock. It is
	// injected rather than read from the build so the lock is reproducible in
	// tests; the CLI passes the real tt version.
	generatedBy string
}

// NewEngine builds an Engine. projectDir anchors path dependencies; generatedBy
// is stamped into lock.generated_by (e.g. "tt 3.4.0").
func NewEngine(adapter Adapter, projectDir, generatedBy string) *Engine {
	return &Engine{
		adapter:     adapter,
		projectDir:  projectDir,
		generatedBy: generatedBy,
	}
}

// Resolve resolves every product of man into a lock, taking the newest version
// that satisfies every constraint. Each product's dependencies are the
// transitive closure of its effective direct dependencies (see effectiveDeps),
// pinned to exact versions in topological order.
//
// The returned warnings are non-fatal diagnostics - notably rocks whose
// registry publishes no md5, whose lock entry then carries no checksum.
//
// The lock's manifest_hash is man.Hash(); bundled_*_version are left empty for
// the packaging phase to fill. generated_by carries the engine's tt version.
//
// This is ResolvePinned with an empty pin set: it is the "pull whatever the
// registry has now" resolution, which is what a bare tt package update wants
// and what a first resolution has no alternative to. Every other caller should
// hold the lock's existing picks - see ResolvePinned.
func (e *Engine) Resolve(
	ctx context.Context, man *manifest.Manifest,
) (*manifest.Lock, []string, error) {
	return e.ResolvePinned(ctx, man, nil)
}

// ResolvePinned resolves man like Resolve, preferring each pinned version where
// it still satisfies every constraint on that rock. PinsFromLock turns an
// existing lock into such a set.
//
// A pin is a preference, not a constraint, and this is where that is enforced.
// A pinned version that no longer fits is dropped and the rock resolves the
// ordinary way; the reason reaches the caller as a warning, never as an error.
// There are two ways a pin can fail to fit, and they are caught in different
// places because the greedy walk discovers them at different times:
//
//   - The pinned version does not satisfy the requirement that triggers the
//     rock's resolution, or the registry no longer serves it. resolveRegistry
//     sees the adapter refuse and re-asks without the pin.
//   - The pinned version satisfies the edge that resolved it but not one found
//     later in the walk. By then its subtree is already resolved, so the walk
//     cannot back out in place; it signals instead, and the pass is discarded
//     and re-run below without that one pin. Each retry drops exactly one pin,
//     so the loop runs at most len(pins) times.
func (e *Engine) ResolvePinned(
	ctx context.Context, man *manifest.Manifest, pins Pins,
) (*manifest.Lock, []string, error) {
	attempt := pins

	var dropped []string

	for {
		lock, warnings, err := e.resolveOnce(ctx, man, attempt)
		if err == nil {
			return lock, append(dropped, warnings...), nil
		}

		var conflict *pinConflictError
		if !errors.As(err, &conflict) {
			return nil, nil, err
		}

		held, pinned := attempt[conflict.name]
		if !pinned {
			// Unreachable: only a pinned pick raises this. Returning the
			// conflict rather than looping keeps a future bug a failed
			// resolution instead of a hang.
			return nil, nil, err
		}

		dropped = append(dropped, fmt.Sprintf(
			"locked version %s of %q cannot satisfy every dependency on it; resolved afresh",
			held, conflict.name))
		attempt = attempt.without(conflict.name)
	}
}

// resolveOnce is one full resolution pass over every product against a fixed
// pin set.
//
// It builds a fresh cache each time it is called, which matters because
// ResolvePinned throws a whole pass away on a pin conflict: the cache carries
// the run-wide warning dedup (resolveCache.markNoMD5, markPinDropped), so
// reusing it across attempts would let a discarded pass mark warnings that the
// surviving pass then owes the caller and never emits.
func (e *Engine) resolveOnce(
	ctx context.Context, man *manifest.Manifest, pins Pins,
) (*manifest.Lock, []string, error) {
	lock := &manifest.Lock{
		LockVersion:      manifest.LockVersion,
		ManifestVersion:  man.ManifestVersion,
		GeneratedBy:      e.generatedBy,
		ManifestHash:     man.Hash(),
		BundledTarantool: "",
		BundledTt:        "",
		BundledTcm:       "",
		Products:         map[string]manifest.LockProduct{},
		DevDependencies:  nil, // Filled by resolveDev once the products are in.
	}

	var warnings []string

	// One cache for the whole run: products that share a dependency resolve,
	// fetch and hash it once, not once per product.
	cache := newResolveCache()

	for _, name := range sortedKeys(man.Products) {
		dependencies, warns, err := e.resolveProduct(ctx, cache, man, man.Products[name], pins)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving product %q: %w", name, err)
		}

		warnings = append(warnings, warns...)
		lock.Products[name] = manifest.LockProduct{Dependencies: dependencies}
	}

	devDependencies, devWarnings, err := e.resolveDev(ctx, man, pins, lock)
	if err != nil {
		return nil, nil, err
	}

	warnings = append(warnings, devWarnings...)
	lock.DevDependencies = devDependencies

	return lock, warnings, nil
}

// resolveDev resolves the closure of [dev_dependencies] against the products
// already resolved into lock. A manifest declaring none resolves to nothing.
//
// Dev dependencies are installed into the same .rocks/ tree as the runtime
// closure, and one tree cannot hold two versions of a rock. So the dev closure
// is resolved with every product's pick pinned - PinsFromLock over the lock
// this very pass just built, layered over the caller's own pins so a dev-only
// rock still holds the version it already had. A dev constraint that cannot
// accept the runtime pick drops that pin and warns, which is the ordinary pin
// semantics: the tree then genuinely holds two versions, and saying so is
// better than refusing to resolve.
//
// The pin retry lives here rather than in ResolvePinned because these pins are
// derived from the pass's own output: dropping one from the caller's set would
// not remove it, since the next pass would re-derive it from the same products.
// Each attempt gets a fresh cache for the reason resolveOnce documents - the
// cache carries the warning dedup, and a discarded attempt must not mark
// warnings the surviving one then owes the caller and never emits.
func (e *Engine) resolveDev(
	ctx context.Context, man *manifest.Manifest, pins Pins, lock *manifest.Lock,
) ([]manifest.LockDependency, []string, error) {
	if len(man.DevDependencies) == 0 {
		return nil, nil, nil
	}

	directs, err := devDeps(man)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving dev dependencies: %w", err)
	}

	attempt := pins.merge(PinsFromLock(lock))

	var dropped []string

	for {
		dependencies, warnings, err := e.resolveClosure(
			ctx, newResolveCache(), directs, attempt)
		if err == nil {
			return dependencies, append(dropped, warnings...), nil
		}

		var conflict *pinConflictError
		if !errors.As(err, &conflict) {
			return nil, nil, fmt.Errorf("resolving dev dependencies: %w", err)
		}

		held, pinned := attempt[conflict.name]
		if !pinned {
			// Unreachable: only a pinned pick raises this. Returning the
			// conflict rather than looping keeps a future bug a failed
			// resolution instead of a hang.
			return nil, nil, fmt.Errorf("resolving dev dependencies: %w", err)
		}

		dropped = append(dropped, fmt.Sprintf(
			"version %s of %q cannot satisfy every dev dependency on it; resolved afresh",
			held, conflict.name))
		attempt = attempt.without(conflict.name)
	}
}

// resolveProduct assembles a product's effective direct dependencies and walks
// them into a pinned, topologically ordered closure. pins is the run's
// preferred-version set; it is read, never written, so every product of a pass
// sees the same one.
func (e *Engine) resolveProduct(
	ctx context.Context,
	cache *resolveCache,
	man *manifest.Manifest,
	product manifest.Product,
	pins Pins,
) ([]manifest.LockDependency, []string, error) {
	directs, err := effectiveDeps(man, product)
	if err != nil {
		return nil, nil, err
	}

	return e.resolveClosure(ctx, cache, directs, pins)
}

// resolveClosure walks a set of direct requirements into a pinned,
// topologically ordered closure. It is the shared body of a product resolution
// and of the dev resolution, which differ only in where their direct
// requirements come from. pins is read, never written, so every closure of a
// pass sees the same one.
func (e *Engine) resolveClosure(
	ctx context.Context, cache *resolveCache, directs []depReq, pins Pins,
) ([]manifest.LockDependency, []string, error) {
	directsByName := make(map[string]depReq, len(directs))
	for _, direct := range directs {
		directsByName[direct.name] = direct
	}

	walk := &walker{
		engine:     e,
		cache:      cache,
		directs:    directsByName,
		pins:       pins,
		pinnedPick: map[string]bool{},
		chosen:     map[string]*resolvedDep{},
		inFlight:   map[string]bool{},
		order:      nil,
		warnings:   nil,
	}

	walkErr := walk.walk(ctx, "", directs, nil)
	if walkErr != nil {
		return nil, nil, walkErr
	}

	out := make([]manifest.LockDependency, 0, len(walk.order))
	for _, name := range walk.order {
		out = append(out, walk.chosen[name].lockDep)
	}

	return out, walk.warnings, nil
}
