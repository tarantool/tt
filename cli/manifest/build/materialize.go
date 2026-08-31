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

// rockInstaller is the registry-install slice of go-luarocks' *client.Rocks.
// Registry dependencies use the Lua backend for full LuaRocks artifact and
// deployment compatibility.
type rockInstaller interface {
	Install(ctx context.Context, name string, opts client.InstallOpts) error
}

// rockBuilder is the path-build slice of go-luarocks' *client.Rocks. Path
// dependencies use the native backend so Build cannot resolve outside the
// already pinned lock closure.
type rockBuilder interface {
	Build(ctx context.Context, specPath string, opts client.BuildOpts) error
}

// materialize realizes a product's pinned closure into the rocks tree, in the
// lock's topological order so every dependency is present before the rock that
// needs it, and then the lock's dev closure into the same tree. This is the
// whole of tt package fetch and step 4 of tt package build.
//
// Registry rocks are installed at their exact locked version with dependency
// resolution off (DepsNone): the closure is already complete and ordered, so no
// rock re-resolves its own deps. Path dependencies are built from the single
// rockspec in their directory; a leaf path dependency that ships no rockspec is
// nothing to build and is skipped.
//
// Dev dependencies go into the same tree because they are meant to be
// requirable from the project exactly like runtime ones - that is what makes
// tt test work - and they are installed after it so a rock in both closures is
// already present at the product's version when the dev list reaches it. Such
// a rock is skipped rather than reinstalled: the resolver pins the dev closure
// to the products' picks, so the two agree by construction, and installing the
// same version twice is work with no effect. Keeping them out of the archive
// is cli/manifest/pack's job, not this one - .rocks/ is the developer's tree
// and holds whatever the last build put there.
func materialize(
	ctx context.Context, registryClient rockInstaller, pathClient rockBuilder,
	projectDir string, prod manifest.LockProduct, dev []manifest.LockDependency,
	servers []string,
) error {
	done := make(map[string]bool, len(prod.Dependencies))

	for _, dep := range prod.Dependencies {
		depErr := materializeDep(ctx, registryClient, pathClient, projectDir, dep, servers)
		if depErr != nil {
			return depErr
		}

		done[dep.Name+"\x00"+dep.Version] = true
	}

	for _, dep := range dev {
		if done[dep.Name+"\x00"+dep.Version] {
			continue
		}

		depErr := materializeDep(ctx, registryClient, pathClient, projectDir, dep, servers)
		if depErr != nil {
			return fmt.Errorf("dev dependency: %w", depErr)
		}

		done[dep.Name+"\x00"+dep.Version] = true
	}

	return nil
}

// materializeDep materializes one locked dependency.
func materializeDep(
	ctx context.Context, registryClient rockInstaller, pathClient rockBuilder,
	projectDir string, dep manifest.LockDependency,
	servers []string,
) error {
	switch dep.Source {
	case sourceRegistry:
		opts := client.InstallOpts{
			Version: dep.Version,
			Servers: servers,
			Deps:    client.DepsNone,
		}

		installErr := registryClient.Install(ctx, dep.Name, opts)
		if installErr != nil {
			return fmt.Errorf("installing %s %s: %w", dep.Name, dep.Version, installErr)
		}

		return nil
	case sourcePath:
		return materializePathDep(ctx, pathClient, projectDir, dep)
	default:
		return fmt.Errorf("dependency %q: %w %q", dep.Name, errUnknownSource, dep.Source)
	}
}

// materializePathDep builds a path dependency from the rockspec in its
// directory. A directory with no rockspec is a leaf pinned by content hash with
// nothing to build; more than one rockspec is ambiguous and is an error.
func materializePathDep(
	ctx context.Context, rockClient rockBuilder, projectDir string, dep manifest.LockDependency,
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
