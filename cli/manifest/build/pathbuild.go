package build

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// pathDirName is the directory of _build/ the path dependencies holding a
// manifest are built in, one directory per dependency.
const pathDirName = "path"

// manifestBuilder builds a path dependency whose directory holds
// app.manifest.toml (resolve.PathManifest) and installs it into the rocks
// tree as a rock. pathManifests is the one a build materializes with.
type manifestBuilder interface {
	buildPathManifest(ctx context.Context, dep manifest.LockDependency) error
}

// pathManifests builds the path dependencies of a closure that hold a
// manifest with the steps tt package build runs on a package of its own: the
// backends of the components of the default product, their layout and
// version.lua. Then it installs the stage as the rock of the dependency's
// package, at the version the lock pins, into the consumer's tree.
//
// What the build writes goes into the consumer's _build/path/<name>/ - the
// stage, rock/<product>/, and the records of the incremental and executable
// components - and not into the dependency's directory, which may be another
// checkout, shared by other consumers. The backends still run in the
// dependency's directory, which is their TT_PROJECT_ROOT, so what a backend
// leaves beside its sources (an output it copies, cargo's target/) is where
// its own build would leave it. Nothing is resolved or installed into the
// dependency's own .rocks/, and its hooks are not run: a hook is a step of
// the dependency's own build, which may do anything to its tree.
type pathManifests struct {
	// opts are the options of the consumer's build: its ProjectDir, the sinks
	// of its diagnostics and the switches of the component builds.
	opts Options
	// adapter is the consumer's: the toolchain flags of the backends and the
	// tree the rocks are installed into.
	adapter *rocks.Adapter
	// pinned holds the versions the consumer's lock pins, which the rock of a
	// dependency declares its own dependencies at.
	pinned manifest.LockProduct
}

// newPathManifests returns the pathManifests of a build with the options opts
// and the adapter adapter, its rocks declaring their dependencies at the
// versions closures pin - the first closure that pins a rock deciding.
func newPathManifests(
	opts Options, adapter *rocks.Adapter, closures ...[]manifest.LockDependency,
) pathManifests {
	return pathManifests{opts: opts, adapter: adapter, pinned: pinnedClosure(closures...)}
}

// pinnedClosure merges closures into one, the first that pins a rock
// deciding its version.
func pinnedClosure(closures ...[]manifest.LockDependency) manifest.LockProduct {
	var merged manifest.LockProduct

	seen := map[string]bool{}

	for _, closure := range closures {
		for _, dep := range closure {
			if seen[dep.Name] {
				continue
			}

			seen[dep.Name] = true
			merged.Dependencies = append(merged.Dependencies, dep)
		}
	}

	return merged
}

// pathState is the directory of the build's own state for the path
// dependency name of the project at projectDir.
func pathState(projectDir, name string) string {
	return filepath.Join(projectDir, buildDirName, pathDirName, name)
}

// buildPathManifest builds the path dependency dep and installs it as a rock.
// Its version has to be the one the lock pins: the lock names the rock that
// is installed, and a build has already re-resolved a lock whose path
// dependency moved (resolve.IsStale), so a mismatch is a lock tt package
// fetch is told to install as it is.
func (p pathManifests) buildPathManifest(ctx context.Context, dep manifest.LockDependency) error {
	return p.buildManifest(ctx, manifestBuild{
		dep:      dep,
		dir:      filepath.Join(p.opts.ProjectDir, dep.Path),
		state:    pathState(p.opts.ProjectDir, dep.Name),
		revision: 1,
		what:     "path dependency",
		stale:    "run tt package resolve to bring the lock back in step",
	})
}

// manifestBuild is one build of a tt package another project depends on: a
// path dependency, or the unpacked source rock of a registry one.
type manifestBuild struct {
	// dep is the locked dependency the package is.
	dep manifest.LockDependency
	// dir is the directory of the package; state the one of its build's
	// state, in the consumer's _build/.
	dir   string
	state string
	// revision is the LuaRocks revision of the rock it is installed as.
	revision int
	// what names the dependency in messages; stale says what to do when its
	// version is not the one the lock pins.
	what  string
	stale string
}

// buildManifest builds the package of build and installs it into the
// consumer's tree as the rock of the dependency, at the version the lock
// pins, which the package has to be at.
func (p pathManifests) buildManifest(ctx context.Context, build manifestBuild) error {
	dep := build.dep

	neighbour, err := resolve.LoadPathManifest(dep.Name, build.dir)
	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	man := neighbour.Manifest
	components := neighbour.Components()

	if len(man.Hooks) > 0 {
		hooks := make([]string, 0, len(man.Hooks))
		for hook := range man.Hooks {
			hooks = append(hooks, hook)
		}

		slices.Sort(hooks)

		p.opts.emit([]string{fmt.Sprintf("%s %q declares the hooks %s; the hooks of a %s "+
			"are not run", build.what, dep.Name, strings.Join(hooks, ", "), build.what)})
	}

	err = preflightBackends(man, components)
	if err != nil {
		return fmt.Errorf("%s %q: %w", build.what, dep.Name, err)
	}

	//nolint:contextcheck // version.Derive spawns git and takes no context.
	ver, _, err := neighbour.Version()
	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	rockVersion, err := rocktree.RockVersionAt(ver.SemVer, build.revision)
	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "%s %q: %w", build.what, dep.Name, err)
	}

	if rockVersion != dep.Version {
		return sdk.Errorf(sdk.ExitFailure, "%s %q is at version %s, but the lock pins %s; %s",
			build.what, dep.Name, rockVersion, dep.Version, build.stale)
	}

	stage := filepath.Join(build.state, stageDirName, neighbour.Product)
	proj := project{dir: build.dir, stateDir: build.state, man: man, product: neighbour.Product}

	_, err = buildComponents(ctx, p.opts, proj, p.adapter, stage, components, ver)
	if err != nil {
		return fmt.Errorf("%s %q: %w", build.what, dep.Name, err)
	}

	return installStage(ctx, p.adapter.Config(), rocktree.Package{
		Name:         dep.Name,
		Version:      ver.SemVer,
		Revision:     build.revision,
		Summary:      man.Package.Description,
		Dependencies: rocktree.PackageDependencies(man, neighbour.Product, p.pinned),
	}, stage)
}

// addPathManifests adds to servers the registries the path dependencies of
// closures that hold a manifest name for their own dependencies
// (resolve.PathManifest.Registries), so a rock the resolver found in such a
// registry is installed from there too. A registry the consumer's manifest
// names for a rock wins, as its declaration does in the resolver.
func (s rockServers) addPathManifests(
	projectDir string, closures ...[]manifest.LockDependency,
) error {
	for _, closure := range closures {
		for _, dep := range closure {
			if dep.Source != sourcePath {
				continue
			}

			dir := filepath.Join(projectDir, dep.Path)

			isManifest, err := resolve.IsPathManifest(dir)
			if err != nil {
				return err
			}

			if !isManifest {
				continue
			}

			neighbour, err := resolve.LoadPathManifest(dep.Name, dir)
			if err != nil {
				return err
			}

			registries, err := neighbour.Registries()
			if err != nil {
				return err
			}

			for name, registry := range registries {
				if _, seen := s.overrides[name]; !seen {
					s.overrides[name] = registry
				}
			}
		}
	}

	return nil
}
