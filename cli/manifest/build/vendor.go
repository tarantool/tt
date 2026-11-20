package build

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// This file is the part of tt package download that is the backends': what
// the builds of the components of a project download themselves - the crates
// of cargo - downloaded ahead into the mirror (sdkbuild.Vendorer), each
// backend into a directory of its own, <mirror>/<backend>, which a build
// whose rock servers have the mirror is given (sdkbuild.Context.Vendored).

// VendorResult is what Vendor downloaded.
type VendorResult struct {
	// Components are the components whose backend's Vendor ran: those of
	// the project by name, then those of its path dependencies, as
	// <dependency>/<component>, in order.
	Components []string
	// Dirs are the directories of the mirror the components downloaded
	// into, <mirror>/<backend>, in order and without repeats.
	Dirs []string
}

// Vendor runs the Vendor of the build of every component of the project in
// opts.ProjectDir whose build is a sdkbuild.Vendorer, in name order, then of
// the components of the path dependencies its lock pins, which its build
// builds, into the directory of its backend in mirror, <mirror>/<backend>:
// tt package download runs it once the rocks of the lock are in mirror. The
// Contexts are those of a build of the project, the components of every
// product's, but Locked, which is set: what the components download is what
// their sources lock. A component whose backend this tt does not have is
// skipped with a warning; the failure of a Vendor fails the run, with exit
// code 2. The manifests are read and checked as a build checks them; nothing
// is built and the lock of the project is not taken, as the download of the
// rocks takes none.
func Vendor(ctx context.Context, opts Options, mirror string) (*VendorResult, error) {
	man, err := readManifest(opts.ProjectDir, opts)
	if err != nil {
		return nil, err
	}

	runs := newProjectRuns(opts, man)

	runs.opts.Locked = true

	result := &VendorResult{Components: nil, Dirs: nil}

	for _, name := range slices.Sorted(maps.Keys(man.Components)) {
		if man.Components[name].Build == nil {
			continue
		}

		//nolint:contextcheck // The version of the package asks git, which takes no context.
		err = vendorComponent(ctx, runs.builder(runs.productOf(name)), name, name, mirror,
			result)
		if err != nil {
			return nil, err
		}
	}

	err = vendorPathDependencies(ctx, runs.opts, mirror, result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// vendorPathDependencies runs Vendor of the components of every path
// dependency the lock of the project of opts pins, as its build would build
// them (pathManifests.buildManifest). A project without a lock has none.
func vendorPathDependencies(
	ctx context.Context, opts Options, mirror string, result *VendorResult,
) error {
	lock, err := loadLock(opts.ProjectDir)
	if errors.Is(err, errNoLock) {
		return nil
	}

	if err != nil {
		return err
	}

	opts.Component, opts.Components, opts.Args = "", nil, nil

	for _, dep := range pathDependencies(lock) {
		dir := manifest.PathFrom(opts.ProjectDir, dep.Path)

		neighbour, err := resolve.LoadPathManifest(dep.Name, dir, opts.backends())
		if err != nil {
			return sdk.Errorf(sdk.ExitFailure, "%w", err)
		}

		proj := project{
			dir: dir, stateDir: pathState(opts.ProjectDir, dep.Name), man: neighbour.Manifest,
			product: neighbour.Product, pinned: manifest.LockProduct{}, fromServer: false,
		}
		stage := filepath.Join(proj.stateDir, stageDirName, neighbour.Product)
		builder := newBuilder(opts, proj, stage, version.Version{})

		for _, name := range neighbour.Components() {
			if neighbour.Manifest.Components[name].Build == nil {
				continue
			}

			err = vendorComponent(ctx, builder, name, dep.Name+"/"+name, mirror, result)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// pathDependencies returns the path dependencies lock pins, of every product
// and the dev closure, each once, in name order.
func pathDependencies(lock *manifest.Lock) []manifest.LockDependency {
	found := map[string]manifest.LockDependency{}

	collect := func(deps []manifest.LockDependency) {
		for _, dep := range deps {
			if dep.Source == sourcePath {
				found[dep.Name] = dep
			}
		}
	}

	for _, product := range slices.Sorted(maps.Keys(lock.Products)) {
		collect(lock.Products[product].Dependencies)
	}

	collect(lock.DevDependencies)

	deps := make([]manifest.LockDependency, 0, len(found))
	for _, name := range slices.Sorted(maps.Keys(found)) {
		deps = append(deps, found[name])
	}

	return deps
}

// vendorComponent runs the Vendor of the build of the component name of
// builds into mirror, when it is a sdkbuild.Vendorer, and adds it to result
// as label.
func vendorComponent(
	ctx context.Context, builds builder, name, label, mirror string, result *VendorResult,
) error {
	run, err := builds.component(name)

	switch {
	case errors.Is(err, buildreg.ErrNoBackend):
		builds.opts.emit([]string{fmt.Sprintf("component %q: %s; what its backend downloads "+
			"is not downloaded", label, err)})

		return nil
	case err != nil:
		return sdk.Errorf(sdk.ExitFailure, "downloading for component %q: %w", label, err)
	}

	vendorer, ok := run.build.(sdkbuild.Vendorer)
	if !ok {
		return nil
	}

	dir := filepath.Join(mirror, run.backend.Name)

	err = vendorer.Vendor(ctx, run.context, dir)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "downloading what component %q builds with: %w",
			label, err)
	}

	result.Components = append(result.Components, label)

	if !slices.Contains(result.Dirs, dir) {
		result.Dirs = append(result.Dirs, dir)
	}

	return nil
}
