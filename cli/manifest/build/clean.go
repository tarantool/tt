package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/projectlock"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// This file is tt package clean: it removes what the builds of a project
// made, and only that.
//
// What the core removes is what is the core's alone: _build/, the build's
// own state - the directories of the intermediate files of the components
// (TT_BUILD_DIR), the records of the incremental builds and of what the
// components made, the stages, the builds of the path dependencies and what
// tt package pack wrote there - and the rock of the package in .rocks/,
// which the build installed from the stage, by the files its rock manifest
// lists. The rocks of the dependency closure stay: they are no result of a
// build but of the lock, which tt package fetch materializes again from the
// network, and the guest packages tt package install put into .rocks/ are no
// one's to make again. Nothing in the sources of the project is removed by
// the core: the files the output of a block lists may be prebuilt files the
// project keeps, and what a tool generates beside the sources only its
// backend knows - its Clean (sdkbuild.Cleaner) removes that - or the
// project, whose [hooks.clean] runs on a clean of the whole project.

// hookClean is the hook tt package clean of the whole project runs.
const hookClean = "clean"

// CleanOptions are what Clean cleans.
type CleanOptions struct {
	// Components narrow the clean to these components of the manifest; none
	// cleans the whole project.
	Components []string
	// DryRun removes nothing and runs no Clean of a backend and no hook: the
	// result says what a clean would remove and run. It takes no lock.
	DryRun bool
}

// CleanResult is what Clean removed and ran, or would have.
type CleanResult struct {
	// Removed are the files and directories removed from _build/, absolute,
	// in order.
	Removed []string
	// Rocks are the versions of the rock of the package removed from
	// .rocks/.
	Rocks []CleanedRock
	// Cleaned are the components whose backend's Clean ran, in name order.
	Cleaned []string
	// Hook reports that [hooks.clean] ran.
	Hook bool
}

// CleanedRock is a version of the rock of the package Clean removed from
// .rocks/, with its files, absolute, as its rock manifest lists them.
type CleanedRock struct {
	Name    string
	Version string
	Files   []string
}

// Clean removes what the builds of the project in opts.ProjectDir made, as
// clean says: for every component it cleans whose build is a
// sdkbuild.Cleaner, the Clean of the build first, then, for a clean of the
// whole project, the clean hook, then what the core made. A clean of the
// whole project removes every entry of _build/ but the lock file; a clean of
// components removes the directory of the intermediate files of each, its
// records and its files in the stages. Either removes the rock of the
// package from .rocks/: it is installed whole, and a rock that still held
// the files of a component cleaned would have require load what the clean
// said was gone; the next build installs it again.
//
// The manifest is read and checked as a build checks it. A component the
// manifest does not declare is an error wrapping manifest.ErrNoComponent,
// with exit code 1; a Clean or a
// hook that fails is one with exit code 2, before the core removes anything.
// A component whose backend this tt does not have is cleaned by the core
// alone, with a warning. The clean holds the lock of the project as a build
// does, but a dry run, which reads only.
func Clean(ctx context.Context, opts Options, clean CleanOptions) (*CleanResult, error) {
	if !clean.DryRun {
		var (
			release func()
			err     error
		)

		ctx, release, err = lockProject(ctx, opts.ProjectDir)
		if err != nil {
			return nil, err
		}

		defer release()
	}

	man, err := readManifest(opts.ProjectDir, opts)
	if err != nil {
		return nil, err
	}

	components, err := cleanedComponents(man, clean.Components)
	if err != nil {
		return nil, err
	}

	result := &CleanResult{Removed: nil, Rocks: nil, Cleaned: nil, Hook: false}
	cleaner := newProjectCleaner(opts, man)

	err = cleaner.runCleaners(ctx, components, clean.DryRun, result)
	if err != nil {
		return nil, err
	}

	stateDir := filepath.Join(opts.ProjectDir, buildDirName)

	if len(clean.Components) == 0 {
		err = cleaner.runHook(ctx, clean.DryRun, result)
		if err == nil {
			result.Removed, err = removeState(stateDir, clean.DryRun)
		}
	} else {
		result.Removed, err = removeComponentsState(stateDir, man, components, clean.DryRun)
	}

	if err != nil {
		return nil, err
	}

	result.Rocks, err = removePackageRock(ctx, opts, man.Package.Name, clean.DryRun)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// cleanedComponents returns the components a clean of names cleans, in name
// order: every component of man for none, and those named otherwise, each a
// component man declares.
func cleanedComponents(man *manifest.Manifest, names []string) ([]string, error) {
	if len(names) == 0 {
		return slices.Sorted(maps.Keys(man.Components)), nil
	}

	var components []string

	for _, name := range names {
		if _, ok := man.Components[name]; !ok {
			return nil, sdk.Errorf(sdk.ExitFailure, "%w %q", manifest.ErrNoComponent, name)
		}

		if !slices.Contains(components, name) {
			components = append(components, name)
		}
	}

	slices.Sort(components)

	return components, nil
}

// projectCleaner makes the Contexts of the Clean of the backends and of the
// clean hook of the package of a project, as a build of it would.
type projectCleaner struct {
	opts Options
	man  *manifest.Manifest
	// version is the version of the package, derived when first asked for;
	// derived is set then.
	version version.Version
	derived bool
}

// newProjectCleaner returns the cleaner of the project of opts, whose
// manifest is man.
func newProjectCleaner(opts Options, man *manifest.Manifest) *projectCleaner {
	// The run asks the build for the Contexts of a build, and builds nothing:
	// it never builds, so the build run has neither Args nor --rebuild.
	opts.Args = nil
	opts.Rebuild = false
	opts.Locked = false

	return &projectCleaner{
		opts:    opts,
		man:     man,
		version: version.Version{},
		derived: false,
	}
}

// builder returns the builder of the product named product. The version is
// that of the package, or none when it cannot be derived: a clean does not
// need one, and a project with no git and no VERSION file is still cleaned.
func (p *projectCleaner) builder(product string) builder {
	if !p.derived {
		ver, err := version.Derive(p.opts.ProjectDir)
		if err == nil {
			ver.Flavor = p.man.Platform.Tarantool.EffectiveFlavor()
			p.version = ver
		}

		p.derived = true
	}

	proj := packageProject(p.opts.ProjectDir, p.man, product)

	proj.pinned = freshPins(p.opts.ProjectDir, p.man, product)

	return newBuilder(p.opts, proj, packageStage(p.opts.ProjectDir, product), p.version)
}

// productOf returns the product the Context of the Clean of the component
// name is that of: the default product when it builds the component, else
// the first product by name that does, else the default one.
func (p *projectCleaner) productOf(name string) string {
	defaultName, product, ok := p.man.DefaultProduct()
	if ok && slices.Contains(product.Components, name) {
		return defaultName
	}

	products := p.man.EffectiveProducts()

	for _, other := range slices.Sorted(maps.Keys(products)) {
		if slices.Contains(products[other].Components, name) {
			return other
		}
	}

	return defaultName
}

// runCleaners runs the Clean of the build of each of components whose build
// is a sdkbuild.Cleaner, or with dryRun records only which would run.
func (p *projectCleaner) runCleaners(
	ctx context.Context, components []string, dryRun bool, result *CleanResult,
) error {
	for _, name := range components {
		if p.man.Components[name].Build == nil {
			continue
		}

		//nolint:contextcheck // The version of the package asks git, which takes no context.
		run, err := p.builder(p.productOf(name)).component(name)

		switch {
		case errors.Is(err, buildreg.ErrNoBackend):
			p.opts.emit([]string{fmt.Sprintf("component %q: %s; what its backend made "+
				"outside _build/ is not cleaned", name, err)})

			continue
		case err != nil:
			return sdk.Errorf(sdk.ExitFailure, "cleaning component %q: %w", name, err)
		}

		cleaner, ok := run.build.(sdkbuild.Cleaner)
		if !ok {
			continue
		}

		result.Cleaned = append(result.Cleaned, name)

		if dryRun {
			continue
		}

		err = cleaner.Clean(ctx, run.context)
		if err != nil {
			return sdk.Errorf(sdk.ExitSystem, "cleaning component %q: %w", name, err)
		}
	}

	return nil
}

// runHook runs the clean hook of the package, when the manifest declares
// one, or with dryRun records only that it would run.
func (p *projectCleaner) runHook(ctx context.Context, dryRun bool, result *CleanResult) error {
	if _, ok := p.man.Hooks[hookClean]; !ok {
		return nil
	}

	result.Hook = true

	if dryRun {
		return nil
	}

	product, _, ok := p.man.DefaultProduct()
	if !ok {
		product = ""
	}

	//nolint:contextcheck // The version of the package asks git, which takes no context.
	err := p.builder(product).runHook(ctx, hookClean)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "%w", err) // runHook names the hook.
	}

	return nil
}

// removeState removes every entry of the build directory stateDir but the
// lock of the project, or with dryRun lists them only, and returns them.
func removeState(stateDir string, dryRun bool) ([]string, error) {
	entries, err := os.ReadDir(stateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", stateDir, err)
	}

	var removed []string

	for _, entry := range entries {
		if entry.Name() == projectlock.FileName {
			continue
		}

		path := filepath.Join(stateDir, entry.Name())

		removed = append(removed, path)

		if dryRun {
			continue
		}

		err = os.RemoveAll(path)
		if err != nil {
			return removed, fmt.Errorf("removing %s: %w", path, err)
		}
	}

	return removed, nil
}

// removeComponentsState removes from the build directory stateDir what the
// builds of components made, or with dryRun lists it only, and returns what
// was there: the directory of the intermediate files, the incremental record
// and the libraries made for linking of each, its files in every stage, and
// its entries in the record
// of what the components made and in the records of the stages.
func removeComponentsState(
	stateDir string, man *manifest.Manifest, components []string, dryRun bool,
) ([]string, error) {
	var removed []string

	for _, name := range components {
		// What the component made for linking, in the directory of each
		// product.
		linkDirs, _ := filepath.Glob(componentLinkDir(stateDir, "*", name))

		for _, path := range append([]string{
			componentBuildDir(stateDir, name), incrementalPath(stateDir, name),
		}, linkDirs...) {
			_, err := os.Lstat(path)
			if err != nil {
				continue
			}

			removed = append(removed, path)

			if !dryRun {
				err = os.RemoveAll(path)
				if err != nil {
					return removed, fmt.Errorf("removing %s: %w", path, err)
				}
			}
		}
	}

	staged, err := removeFromStages(stateDir, components, dryRun)

	removed = append(removed, staged...)

	if err != nil || dryRun {
		return removed, err
	}

	results := readResults(stateDir, nil)

	recorded := slices.ContainsFunc(components, func(name string) bool {
		_, ok := results[name]

		return ok
	})
	if !recorded {
		return removed, nil
	}

	for _, name := range components {
		delete(results, name)
	}

	_, err = writeResults(stateDir, man, results)

	return removed, err
}

// removeFromStages removes the files the records of the stages under
// stateDir say components put there, and their entries in the records, or
// with dryRun lists the files only, and returns those that were there.
func removeFromStages(stateDir string, components []string, dryRun bool) ([]string, error) {
	records, err := filepath.Glob(filepath.Join(stateDir, stageDirName, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("finding the stages: %w", err)
	}

	var removed []string

	for _, path := range records {
		stage := strings.TrimSuffix(path, ".json")

		record, ok := readStageRecord(stage)
		if !ok {
			continue
		}

		changed := false

		for _, name := range components {
			files, ok := record.Components[name]
			if !ok {
				continue
			}

			for _, file := range joinDir(stage, slices.Concat(files.Written, files.LaidOut)) {
				_, err := os.Lstat(file)
				if err != nil {
					continue
				}

				removed = append(removed, file)

				if !dryRun {
					err = os.Remove(file)
					if err != nil {
						return removed, fmt.Errorf("removing %s: %w", file, err)
					}
				}
			}

			delete(record.Components, name)

			changed = true
		}

		if changed && !dryRun {
			err = writeStageRecord(stage, record)
			if err != nil {
				return removed, err
			}
		}
	}

	return removed, nil
}

// removePackageRock removes every version of the rock name, the package's,
// from the rocks tree of the project of opts, or with dryRun lists them
// only, and returns them with their files.
func removePackageRock(
	ctx context.Context, opts Options, name string, dryRun bool,
) ([]CleanedRock, error) {
	treeDir := filepath.Join(opts.ProjectDir, rocksDirName)

	_, err := os.Stat(treeDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree: treeDir, WorkingDir: opts.ProjectDir, Servers: nil, Logger: opts.Logger,
	}))

	rocksTree, err := openTree(adapter.Config())
	if err != nil {
		return nil, err
	}

	if dryRun {
		view, err := rocksTree.View(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading the rocks tree %s: %w", treeDir, err)
		}

		return rocksOf(view, name), nil
	}

	var removed []CleanedRock

	err = rocksTree.Update(ctx, func(change *tree.Tx) error {
		removed = rocksOf(change.View(), name)

		for _, rock := range removed {
			err := change.Remove(tree.RockRef{Name: rock.Name, Version: rock.Version})
			if err != nil {
				return err //nolint:wrapcheck // Wrapped with the Update's error below.
			}
		}

		return nil
	})
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitFailure, "removing %s from %s: %w", name, treeDir, err)
	}

	return removed, nil
}

// rocksOf returns the versions of the rock name view has, each with its
// files; a version whose rock manifest cannot be read is listed with none.
func rocksOf(view *tree.View, name string) []CleanedRock {
	versions := view.Versions(name)
	found := make([]CleanedRock, 0, len(versions))

	for _, ver := range versions {
		rock := CleanedRock{Name: name, Version: ver, Files: nil}

		files, err := view.Files(tree.RockRef{Name: name, Version: ver})
		if err == nil {
			for _, file := range files {
				rock.Files = append(rock.Files, file.Path)
			}
		}

		found = append(found, rock)
	}

	return found
}
