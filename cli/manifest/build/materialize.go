package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/cli/manifest"
)

// Lock dependency sources (mirrors the closed set the resolver writes).
const (
	sourceRegistry = "registry"
	sourcePath     = "path"
)

// rockClient is the slice of go-luarocks' *client.Rocks the materializer drives:
// fetch-and-build a pinned registry rock into the tree, or build a path
// dependency's rockspec in place. *client.Rocks satisfies it; tests fake it so
// the lock walk is exercised without a registry or a compiler.
type rockClient interface {
	Install(ctx context.Context, name string, opts client.InstallOpts) error
	Build(ctx context.Context, specPath string, opts client.BuildOpts) error
}

// materialize realizes a product's pinned closure into the rocks tree, in the
// lock's topological order so every dependency is present before the rock that
// needs it. This is the whole of tt package fetch and step 4 of tt package
// build.
//
// Registry rocks are installed at their exact locked version with dependency
// resolution off (DepsNone): the closure is already complete and ordered, so no
// rock re-resolves its own deps. Path dependencies are built from the single
// rockspec in their directory; a leaf path dependency that ships no rockspec is
// nothing to build and is skipped.
func materialize(
	ctx context.Context, client rockClient, projectDir string, prod manifest.LockProduct,
) error {
	for _, dep := range prod.Dependencies {
		depErr := materializeDep(ctx, client, projectDir, dep)
		if depErr != nil {
			return depErr
		}
	}

	return nil
}

// materializeDep materializes one locked dependency.
func materializeDep(
	ctx context.Context, rockClient rockClient, projectDir string, dep manifest.LockDependency,
) error {
	switch dep.Source {
	case sourceRegistry:
		opts := client.InstallOpts{Version: dep.Version, Servers: nil, Deps: client.DepsNone}

		installErr := rockClient.Install(ctx, dep.Name, opts)
		if installErr != nil {
			return fmt.Errorf("installing %s %s: %w", dep.Name, dep.Version, installErr)
		}

		return nil
	case sourcePath:
		return materializePathDep(ctx, rockClient, projectDir, dep)
	default:
		return fmt.Errorf("dependency %q: %w %q", dep.Name, errUnknownSource, dep.Source)
	}
}

// materializePathDep builds a path dependency from the rockspec in its
// directory. A directory with no rockspec is a leaf pinned by content hash with
// nothing to build; more than one rockspec is ambiguous and is an error.
func materializePathDep(
	ctx context.Context, rockClient rockClient, projectDir string, dep manifest.LockDependency,
) error {
	dir := filepath.Join(projectDir, dep.Path)

	specs, err := filepath.Glob(filepath.Join(dir, "*.rockspec"))
	if err != nil {
		return fmt.Errorf("path dependency %q: %w", dep.Name, err)
	}

	switch len(specs) {
	case 0:
		return nil
	case 1:
		buildErr := rockClient.Build(ctx, specs[0], client.BuildOpts{Keep: false})
		if buildErr != nil {
			return fmt.Errorf("building path dependency %q: %w", dep.Name, buildErr)
		}

		return nil
	default:
		return fmt.Errorf("path dependency %q: %w (%d in %s)",
			dep.Name, errAmbiguousRockspec, len(specs), dir)
	}
}
