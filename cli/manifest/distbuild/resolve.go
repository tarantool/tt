package distbuild

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// pseudoRevRe pulls the abbreviated commit out of a pseudo-version.
var pseudoRevRe = regexp.MustCompile(`[-.]\d{14}-([0-9a-f]{12})(?:\+[0-9A-Za-z.-]+)?$`)

// core is the tt core a distribution is built on.
type core struct {
	// Module is the Go module of the core.
	Module string
	// Version is the exact version of the core; empty for a local core.
	Version string
	// Revision is the commit Version is; empty for a local core.
	Revision string
	// Dir is the local core, absolute; empty for a versioned one.
	Dir string
	// Path is the local core as the manifest names it.
	Path string
	// GoVersion is the go directive of the core's go.mod.
	GoVersion string
	// Repository are the other modules of the core's repository, by module
	// path: the modules the core's go.mod replaces with its own
	// directories, which a module depending on the core cannot see.
	Repository []repoModule
}

// repoModule is a module of the core's repository, pinned to the core.
type repoModule struct {
	// Module is the module path.
	Module string
	// Version is its exact version at the core's revision; empty for a
	// local core.
	Version string
	// Dir is its directory in a local core; empty for a versioned one.
	Dir string
}

// module is a tt module of the distribution.
type module struct {
	// Name is the name the manifest lists it under.
	Name string
	// Module is the Go module path.
	Module string
	// Version is the exact version of a module outside the core's
	// repository fetched from a module proxy; empty otherwise.
	Version string
	// Dir is the local module, absolute; empty otherwise.
	Dir string
	// Path is the local module as the manifest names it.
	Path string
	// Package is the import path of the constructor's package.
	Package string
	// Symbol is the constructor.
	Symbol string
}

// resolver turns the manifest into exact modules.
type resolver struct {
	gocmd goCommand
	// dir is the manifest's directory.
	dir string
	// work is the directory the go command runs in.
	work string
	// env is the environment of the go command.
	env []string
	// pinned is the lock to take versions from; nil re-resolves everything.
	pinned *manifest.DistributionLock
	// unpinnedCore resolves the core and its repository's modules again
	// even when pinned is set: the core is overridden.
	unpinnedCore bool
	// runningCore is the version of the core of the tt that builds; empty
	// when unknown.
	runningCore string
}

// resolveCore finds the core the manifest asks for.
func (r *resolver) resolveCore(ctx context.Context, dist *manifest.Distribution) (core, error) {
	if dist.Core.Path != "" {
		return r.localCore(ctx, dist)
	}

	query := dist.Core.Tt
	if r.pinnedCore() != nil {
		query = r.pinnedCore().Version
	}

	if query == "" {
		query = r.runningCore
		if query == "" || strings.Contains(query, "+") {
			return core{}, sdk.Errorf(sdk.ExitFailure, "core.tt: not set, and the core of "+
				"this tt has no version Go can fetch: set [core].tt or [core].path")
		}
	}

	info, err := listModule(ctx, r.gocmd, r.work, r.env, dist.CoreModule(), query)
	if err != nil {
		return core{}, fmt.Errorf("core.tt: %w", err)
	}

	gomod, err := readGoMod(ctx, r.gocmd, r.work, r.env, info.GoMod)
	if err != nil {
		return core{}, err
	}

	resolved := core{
		Module:     dist.CoreModule(),
		Version:    info.Version,
		Revision:   revision(info),
		Dir:        "",
		Path:       "",
		GoVersion:  gomod.Go,
		Repository: nil,
	}

	resolved.Repository, err = r.pinRepository(ctx, resolved, gomod)
	if err != nil {
		return core{}, err
	}

	return resolved, nil
}

// localCore reads the core from a local checkout.
func (r *resolver) localCore(ctx context.Context, dist *manifest.Distribution) (core, error) {
	dir := r.abs(dist.Core.Path)

	gomod, err := readGoMod(ctx, r.gocmd, r.work, r.env, filepath.Join(dir, "go.mod"))
	if err != nil {
		return core{}, fmt.Errorf("core.path: %w", err)
	}

	if gomod.Module.Path != dist.CoreModule() {
		return core{}, sdk.Errorf(sdk.ExitFailure,
			"core.path: %s holds module %s, not %s: set [core].module",
			dist.Core.Path, gomod.Module.Path, dist.CoreModule())
	}

	resolved := core{
		Module:     dist.CoreModule(),
		Version:    "",
		Revision:   "",
		Dir:        dir,
		Path:       dist.Core.Path,
		GoVersion:  gomod.Go,
		Repository: nil,
	}

	for _, rep := range localReplaces(gomod) {
		resolved.Repository = append(resolved.Repository, repoModule{
			Module:  rep.Old.Path,
			Version: "",
			Dir:     filepath.Join(dir, rep.New.Path),
		})
	}

	sortRepository(resolved.Repository)

	return resolved, nil
}

// pinRepository pins every module of the core's repository the core's
// go.mod replaces with a directory of the repository to the core's
// revision.
//
// The core requires them as v0.0.0 and finds them through those replace
// directives; a module that depends on the core does not see the
// directives, so the build pins the modules itself, at the commit the core
// is: the modules of one repository are one version.
func (r *resolver) pinRepository(
	ctx context.Context, resolved core, gomod goModFile,
) ([]repoModule, error) {
	var repo []repoModule

	for _, rep := range localReplaces(gomod) {
		if strings.HasPrefix(filepath.ToSlash(filepath.Clean(rep.New.Path)), "../") {
			// Not a directory of the core's repository: nothing to pin it to.
			continue
		}

		version := ""
		if r.pinnedCore() != nil {
			version = r.pinnedCore().Repository[rep.Old.Path]
		}

		if version == "" {
			if resolved.Revision == "" {
				return nil, sdk.Errorf(sdk.ExitFailure,
					"core.tt: cannot tell the commit of %s@%s to pin %s to",
					resolved.Module, resolved.Version, rep.Old.Path)
			}

			info, err := listModule(ctx, r.gocmd, r.work, r.env, rep.Old.Path, resolved.Revision)
			if err != nil {
				return nil, err
			}

			version = info.Version
		}

		repo = append(repo, repoModule{Module: rep.Old.Path, Version: version, Dir: ""})
	}

	sortRepository(repo)

	return repo, nil
}

// resolveModules resolves the tt modules of the manifest, in the order of
// their names.
func (r *resolver) resolveModules(
	ctx context.Context, dist *manifest.Distribution, resolved core,
) ([]module, error) {
	modules := make([]module, 0, len(dist.Dependencies))

	for _, name := range sortedNames(dist.Dependencies) {
		dep := dist.Dependencies[name]
		field := "dependencies." + name

		var (
			mod module
			err error
		)

		if dep.IsPath() {
			mod, err = r.localModule(ctx, name, dep)
		} else {
			mod, err = r.registryModule(ctx, name, dep, resolved)
		}

		if err != nil {
			return nil, fmt.Errorf("%s: %w", field, err)
		}

		if mod.Package != mod.Module && !strings.HasPrefix(mod.Package, mod.Module+"/") {
			return nil, sdk.Errorf(sdk.ExitFailure, "%s.package: %q is not a package of %q",
				field, mod.Package, mod.Module)
		}

		modules = append(modules, mod)
	}

	return modules, nil
}

// localModule reads a tt module from a local directory.
func (r *resolver) localModule(
	ctx context.Context, name string, dep manifest.ModuleDependency,
) (module, error) {
	dir := r.abs(dep.Path)

	gomod, err := readGoMod(ctx, r.gocmd, r.work, r.env, filepath.Join(dir, "go.mod"))
	if err != nil {
		return module{}, err
	}

	if dep.Module != "" && dep.Module != gomod.Module.Path {
		return module{}, sdk.Errorf(sdk.ExitFailure, "%s holds module %s, not %s",
			dep.Path, gomod.Module.Path, dep.Module)
	}

	return module{
		Name:    name,
		Module:  gomod.Module.Path,
		Version: "",
		Dir:     dir,
		Path:    dep.Path,
		Package: packageOf(dep, gomod.Module.Path),
		Symbol:  dep.SymbolValue(),
	}, nil
}

// registryModule resolves a tt module fetched by the go command. A module of
// the core's repository follows the core; any other is resolved to an exact
// version, or taken from the lock.
func (r *resolver) registryModule(
	ctx context.Context, name string, dep manifest.ModuleDependency, resolved core,
) (module, error) {
	mod := module{
		Name:    name,
		Module:  dep.Module,
		Version: "",
		Dir:     "",
		Path:    "",
		Package: packageOf(dep, dep.Module),
		Symbol:  dep.SymbolValue(),
	}

	if resolved.inRepository(dep.Module) {
		if dep.Version != "" {
			return module{}, sdk.Errorf(sdk.ExitFailure,
				"%s is a module of the core's repository and follows the core: "+
					"drop its version", dep.Module)
		}

		return mod, nil
	}

	if r.pinned != nil {
		locked, ok := r.pinned.Modules[name]
		if ok && locked.Module == dep.Module && locked.Version != "" {
			mod.Version = locked.Version

			return mod, nil
		}
	}

	query := dep.Version
	if query == "" {
		query = manifest.LatestVersion
	}

	info, err := listModule(ctx, r.gocmd, r.work, r.env, dep.Module, query)
	if err != nil {
		return module{}, err
	}

	mod.Version = info.Version

	return mod, nil
}

// pinnedCore returns the locked core, or nil when the core is resolved
// again.
func (r *resolver) pinnedCore() *manifest.LockedCore {
	if r.pinned == nil || r.unpinnedCore {
		return nil
	}

	return &r.pinned.Core
}

// abs returns path, relative to the manifest's directory, as an absolute
// path.
func (r *resolver) abs(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(r.dir, path)
}

// inRepository reports whether path is a module of the core's repository.
func (c core) inRepository(path string) bool {
	return slices.ContainsFunc(c.Repository, func(m repoModule) bool { return m.Module == path })
}

// localReplaces returns the replace directives of gomod that replace a
// module with a directory given relative to the go.mod.
func localReplaces(gomod goModFile) []goModReplace {
	var reps []goModReplace

	for _, rep := range gomod.Replace {
		if rep.New.Version != "" || filepath.IsAbs(rep.New.Path) {
			continue
		}

		if !strings.HasPrefix(rep.New.Path, "./") && !strings.HasPrefix(rep.New.Path, "../") {
			continue
		}

		reps = append(reps, rep)
	}

	return reps
}

// revision returns the commit a module version is: the one the module
// source reports, or the one a pseudo-version names.
func revision(info moduleInfo) string {
	if info.Origin != nil && info.Origin.Hash != "" {
		return info.Origin.Hash
	}

	match := pseudoRevRe.FindStringSubmatch(info.Version)
	if match == nil {
		return ""
	}

	return match[1]
}

// packageOf returns the import path of the constructor's package of dep, a
// module of path modulePath.
func packageOf(dep manifest.ModuleDependency, modulePath string) string {
	if dep.Package != "" {
		return dep.Package
	}

	return modulePath
}

// sortRepository orders the modules of the core's repository by path.
func sortRepository(repo []repoModule) {
	slices.SortFunc(repo, func(a, b repoModule) int { return strings.Compare(a.Module, b.Module) })
}

// sortedNames returns the keys of deps in order.
func sortedNames(deps map[string]manifest.ModuleDependency) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}
