package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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

// errLicenseEntry reports a license_files entry of the package another
// project builds that names no file of the package.
var errLicenseEntry = errors.New("the entry leads out of the package")

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
// stage, rock/<product>/, the records of the incremental and executable
// components, the TT_BUILD_DIR of each component, the build tree of cmake in
// it, and the target directory of cargo - and not into the dependency's
// directory, which may be another checkout, shared by other consumers. The
// backends still run in the dependency's directory, which is their
// TT_PROJECT_ROOT, so what a backend leaves beside its sources (an output it
// copies, the Cargo.lock cargo writes) is where its own build would leave
// it. Nothing is resolved or installed into the dependency's own .rocks/,
// and its hooks are not run: a hook is a step of the dependency's own build,
// which may do anything to its tree.
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
		dep:       dep,
		dir:       manifest.PathFrom(p.opts.ProjectDir, dep.Path),
		state:     pathState(p.opts.ProjectDir, dep.Name),
		revision:  1,
		what:      "path dependency",
		stale:     "run tt package resolve to bring the lock back in step",
		warnHooks: false,
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
	// warnHooks has the build warn that the hooks the package declares are
	// not run. Those of a path dependency the resolver warns of, when it
	// reads its manifest (resolve.PathManifest.Warnings), rather than every
	// build; a source rock is not resolved from its manifest.
	warnHooks bool
}

// buildManifest builds the package of build and installs it into the
// consumer's tree as the rock of the dependency, at the version the lock
// pins, which the package has to be at, with its license files in doc/ of the
// rock (licenseDocs), as tt package pack puts them into its binary rock: the
// rock is the one its release installs, and a .tt archive of the consumer
// carries the license of what it ships.
func (p pathManifests) buildManifest(ctx context.Context, build manifestBuild) error {
	dep := build.dep

	neighbour, err := resolve.LoadPathManifest(dep.Name, build.dir)
	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	man := neighbour.Manifest
	components := neighbour.Components()

	if build.warnHooks && len(man.Hooks) > 0 {
		p.opts.emit([]string{fmt.Sprintf("%s %q declares the hooks %s; the hooks of a %s "+
			"are not run", build.what, dep.Name,
			strings.Join(slices.Sorted(maps.Keys(man.Hooks)), ", "), build.what)})
	}

	proj := project{dir: build.dir, stateDir: build.state, man: man, product: neighbour.Product}

	err = preflightBackends(proj, components, p.opts.Tarantool)
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

	_, err = buildComponents(ctx, p.opts, proj, p.adapter, stage, components, ver)
	if err != nil {
		return fmt.Errorf("%s %q: %w", build.what, dep.Name, err)
	}

	files := stage

	if len(man.Package.LicenseFiles) > 0 {
		var cleanup func()

		files, cleanup, err = rocktree.WithDocs(build.state, stage, func(doc string) error {
			return p.licenseDocs(build, man.Package.LicenseFiles, doc)
		})
		if err != nil {
			return fmt.Errorf("%s %q: %w", build.what, dep.Name, err)
		}

		defer cleanup()
	}

	return installStage(ctx, p.adapter.Config(), rocktree.Package{
		Name:         dep.Name,
		Version:      ver.SemVer,
		Revision:     build.revision,
		Summary:      man.Package.Description,
		Dependencies: rocktree.PackageDependencies(man, neighbour.Product, p.pinned),
	}, files)
}

// licenseDocs copies the license files of the package of build, the entries
// of its license_files, into doc, each at its path in the package, as
// tt package pack copies them into doc/ of its binary rock
// (rocktree.CopyEntry). An entry leading out of the package fails it. One
// the package does not hold is a warning, and the rock is installed without
// it: the consumer cannot mend the package, a source rock from a server
// least of all.
func (p pathManifests) licenseDocs(build manifestBuild, entries []string, doc string) error {
	for _, entry := range entries {
		clean := filepath.Clean(entry)
		if clean == "." || !filepath.IsLocal(clean) {
			return sdk.Errorf(sdk.ExitFailure, "license_files entry %q: %w",
				entry, errLicenseEntry)
		}

		err := rocktree.CopyEntry(build.dir, clean, doc)
		if errors.Is(err, fs.ErrNotExist) {
			p.opts.emit([]string{fmt.Sprintf("%s %q has no %s, a license file of its "+
				"manifest; the rock is installed without it", build.what, build.dep.Name, entry)})

			continue
		}

		if err != nil {
			return fmt.Errorf("license_files entry %q: %w", entry, err)
		}
	}

	return nil
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

			dir := manifest.PathFrom(projectDir, dep.Path)

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
