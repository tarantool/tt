// Package resolve is the dependency-resolution engine for tt packages: it turns
// the declared constraints of a manifest into a pinned closure and builds the
// lock (app.manifest.lock) - declared, resolved, locked.
//
// Resolution runs per product: each product gets its own closure over the
// newest versions that satisfy every constraint. The engine takes an already
// parsed manifest plus an adapter over lib/luarocks; it never touches the
// network itself. The adapter (cli/manifest/rocks) queries registries, fetches
// rockspecs and reports source checksums; the policy - which versions, by which
// product, what lands in the lock - lives here.
//
// Materializing .rocks/ from a lock, editing the manifest on add/remove and
// bundling runtimes into the lock are other packages' work. The tt package
// resolve/update/deps commands are thin wrappers over this engine.
package resolve

import (
	"context"
	"fmt"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/rocks"
	luarocks "github.com/tarantool/tt/lib/luarocks"
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

// Resolve resolves every product of m into a lock. Each product's dependencies
// are the transitive closure of its effective direct dependencies (see
// effectiveDeps), pinned to exact versions in topological order.
//
// The returned warnings are non-fatal diagnostics - notably rocks whose
// registry publishes no md5, whose lock entry then carries no checksum.
//
// The lock's manifest_hash is m.Hash(); bundled_*_version are left empty for
// the packaging phase to fill. generated_by carries the engine's tt version.
func (e *Engine) Resolve(
	ctx context.Context, man *manifest.Manifest,
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
	}

	var warnings []string

	// One cache for the whole run: products that share a dependency resolve,
	// fetch and hash it once, not once per product.
	cache := newResolveCache()

	for _, name := range sortedKeys(man.Products) {
		dependencies, warns, err := e.resolveProduct(ctx, cache, man, man.Products[name])
		if err != nil {
			return nil, nil, fmt.Errorf("resolving product %q: %w", name, err)
		}

		warnings = append(warnings, warns...)
		lock.Products[name] = manifest.LockProduct{Dependencies: dependencies}
	}

	return lock, warnings, nil
}

// resolveProduct assembles a product's effective direct dependencies and walks
// them into a pinned, topologically ordered closure.
func (e *Engine) resolveProduct(
	ctx context.Context, cache *resolveCache, man *manifest.Manifest, product manifest.Product,
) ([]manifest.LockDependency, []string, error) {
	directs, err := effectiveDeps(man, product)
	if err != nil {
		return nil, nil, err
	}

	directsByName := make(map[string]depReq, len(directs))
	for _, direct := range directs {
		directsByName[direct.name] = direct
	}

	walk := &walker{
		engine:   e,
		cache:    cache,
		directs:  directsByName,
		chosen:   map[string]*resolvedDep{},
		inFlight: map[string]bool{},
		order:    nil,
		warnings: nil,
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
