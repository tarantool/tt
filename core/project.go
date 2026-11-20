package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/sdk/v3/scope"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/rockindex"
)

// Root returns the project the command works on: cmd.ProjectRootDir, the
// project the project commands of tt work on.
func (project) Root() (string, error) {
	return cmd.ProjectRootDir()
}

// Manifest parses app.manifest.toml of Root as tt package does and returns
// what it declares in the SDK's terms. A missing manifest is
// sdk.ErrNotFound as well.
func (p project) Manifest() (sdk.Manifest, error) {
	root, err := p.Root()
	if err != nil {
		return sdk.Manifest{}, err
	}

	path := filepath.Join(root, scope.ManifestFileName)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return sdk.Manifest{}, notFoundError{err: fmt.Errorf("reading %s: %w", path, err)}
	}

	if err != nil {
		return sdk.Manifest{}, fmt.Errorf("reading %s: %w", path, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return sdk.Manifest{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	return manifestOf(man), nil
}

// manifestOf is what man declares in the SDK's terms, sharing nothing with
// it.
func manifestOf(man *manifest.Manifest) sdk.Manifest {
	view := sdk.Manifest{
		Package:         man.Package.Name,
		Dependencies:    dependenciesOf(man.Dependencies),
		DevDependencies: dependenciesOf(man.DevDependencies),
		Components:      make(map[string]sdk.ManifestComponent, len(man.Components)),
	}

	for name, component := range man.Components {
		declared := sdk.ManifestComponent{
			Path:      component.Path,
			Include:   component.Include,
			Exclude:   component.Exclude,
			Namespace: component.EffectiveNamespace(man.Package.Name),
			Build:     nil,
		}

		if component.Build != nil {
			declared.Build = buildOf(*component.Build)
		}

		view.Components[name] = declared
	}

	return view.Clone()
}

// buildOf is a build block in the SDK's terms.
func buildOf(block manifest.Build) *sdk.ManifestBuild {
	var output []sdk.ManifestOutput

	if block.Output != nil {
		output = make([]sdk.ManifestOutput, 0, len(block.Output))
	}

	for _, entry := range block.Output {
		output = append(output, sdk.ManifestOutput{Path: entry.Path, To: entry.To})
	}

	return &sdk.ManifestBuild{
		Backend:     block.Backend,
		Cwd:         block.Cwd,
		Env:         block.Env,
		Output:      output,
		Incremental: block.IncrementalValue(),
		Static:      block.StaticValue(),
		Fields:      block.Rest,
	}
}

// dependenciesOf is deps in the SDK's terms.
func dependenciesOf(deps map[string]manifest.Dependency) map[string]sdk.ManifestDependency {
	view := make(map[string]sdk.ManifestDependency, len(deps))

	for name, dep := range deps {
		view[name] = sdk.ManifestDependency{
			Version: dep.Version, Registry: dep.Registry, Path: dep.Path,
		}
	}

	return view
}

// AddComponent declares component in the manifest of Root:
// cmd.AddComponent. A missing manifest is sdk.ErrNotFound as well.
func (project) AddComponent(ctx context.Context, component sdk.Component) error {
	return manifestError(cmd.AddComponent(ctx, component))
}

// UpdateComponent changes a component of the manifest of Root:
// cmd.UpdateComponent. A missing manifest and a component it does not
// declare are sdk.ErrNotFound as well.
func (project) UpdateComponent(ctx context.Context, update sdk.ComponentUpdate) error {
	return manifestError(cmd.UpdateComponent(ctx, update))
}

// RemoveComponent removes a component from the manifest of Root:
// cmd.RemoveComponent. A missing manifest and a component it does not
// declare are sdk.ErrNotFound as well.
func (project) RemoveComponent(ctx context.Context, name string) error {
	return manifestError(cmd.RemoveComponent(ctx, name))
}

// manifestError is err, of an edit of the manifest of Root, as the SDK has
// it: a missing manifest and a component it does not declare are
// sdk.ErrNotFound as well.
func manifestError(err error) error {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, manifest.ErrNoComponent) {
		return notFoundError{err: err}
	}

	return err
}

// Resolve re-resolves the lock of Root: cmd.ResolveProject, its result told
// in the SDK's terms.
func (project) Resolve(ctx context.Context, opts sdk.ResolveOptions) (sdk.ResolveChange, error) {
	result, err := cmd.ResolveProject(ctx, opts.Registries)
	if err != nil {
		return sdk.ResolveChange{}, manifestError(packageError(err))
	}

	return sdk.ResolveChange{Moves: movesOf(result.Moves)}, nil
}

// ReadLock reads the lock of Root: cmd.ReadProjectLock, in the SDK's terms.
// A missing manifest or lock is sdk.ErrNotFound as well.
func (project) ReadLock() (sdk.LockFile, error) {
	read, err := cmd.ReadProjectLock()
	if err != nil {
		return sdk.LockFile{}, manifestError(err)
	}

	view := sdk.LockFile{
		Fresh:           !read.Stale,
		Reason:          read.Reason,
		DefaultProduct:  "",
		Products:        make(map[string]map[string]sdk.LockedRock, len(read.Lock.Products)),
		DevDependencies: lockedRocks(read.Lock.DevDependencies),
	}

	if name, _, ok := read.Manifest.DefaultProduct(); ok {
		view.DefaultProduct = name
	}

	for name, product := range read.Lock.Products {
		view.Products[name] = lockedRocks(product.Dependencies)
	}

	return view, nil
}

// lockedRocks are the rocks of closure in the SDK's terms, by name.
func lockedRocks(closure []manifest.LockDependency) map[string]sdk.LockedRock {
	rocks := make(map[string]sdk.LockedRock, len(closure))
	for _, dep := range closure {
		rocks[dep.Name] = sdk.LockedRock{Version: dep.Version, Path: dep.Path}
	}

	return rocks
}

// Build builds the package of Root as tt package build does:
// cmd.BuildProject, its result told in the SDK's terms.
func (project) Build(ctx context.Context, opts sdk.BuildOptions) (sdk.BuildResult, error) {
	result, err := cmd.BuildProject(ctx, opts)
	if err != nil {
		return sdk.BuildResult{}, manifestError(err)
	}

	built := sdk.BuildResult{
		Product:    result.Product,
		Version:    result.Version.SemVer,
		Components: make(map[string]sdk.BuiltComponent, len(result.Built)),
	}

	for name, component := range result.Built {
		built.Components[name] = sdk.BuiltComponent{
			Modules:  component.Modules,
			Data:     component.Data,
			Programs: component.Programs,
			UpToDate: component.UpToDate,
		}
	}

	return built, nil
}

// BuildContext returns the Context of the build of a component of Root:
// cmd.ComponentContext.
func (project) BuildContext(
	_ context.Context, opts sdk.BuildContextOptions,
) (*sdkbuild.Context, sdkbuild.Build, error) {
	facts, bld, err := cmd.ComponentContext(opts)
	if err != nil {
		return nil, nil, manifestError(err)
	}

	return facts, bld, nil
}

// Clean removes what the builds of Root made, as tt package clean does:
// cmd.CleanProject, its result told in the SDK's terms. A component the
// manifest does not declare is sdk.ErrNotFound as well.
func (project) Clean(ctx context.Context, opts sdk.CleanOptions) (sdk.CleanResult, error) {
	result, err := cmd.CleanProject(ctx, opts)
	if err != nil {
		return sdk.CleanResult{}, manifestError(err)
	}

	cleaned := sdk.CleanResult{
		Removed: slices.Clone(result.Removed),
		Cleaned: slices.Clone(result.Cleaned),
		Hook:    result.Hook,
	}

	for _, rock := range result.Rocks {
		cleaned.Removed = append(cleaned.Removed, rock.Files...)
	}

	return cleaned, nil
}

// Lock takes the lock of Root: cmd.LockProject.
func (project) Lock(ctx context.Context) (context.Context, func(), error) {
	return cmd.LockProject(ctx)
}

// ScopeDir returns .tarantool of Root.
func (p project) ScopeDir() (string, error) {
	root, err := p.Root()
	if err != nil {
		return "", err
	}

	return scope.ProjectRoot(root), nil
}

// PrepareEnvironment writes .env/activate.sh and deactivate.sh of Root when
// they are missing: cmd.PrepareProjectEnvironment.
func (project) PrepareEnvironment() error {
	return cmd.PrepareProjectEnvironment()
}

// rockServers write rock servers, as tt package index does.
type rockServers struct {
	// log receives what LuaRocks warns of while it writes the manifests.
	log *slog.Logger
}

// Index makes dir a rock server: rockindex.MakeServer.
func (r rockServers) Index(ctx context.Context, dir string, opts sdk.IndexOptions) error {
	_, err := rockindex.MakeServer(ctx, dir, rockindex.ServerOptions{
		HTML: false, IfIndexed: opts.IfIndexed, Warn: func(msg string) { r.log.Debug(msg) },
	})

	return err
}
