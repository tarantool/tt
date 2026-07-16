// Package build is the orchestration layer of the tt manifest pipeline: it runs
// the full build cycle of a package (tt package build) and its fetch-only slice
// (tt package fetch). It stitches together the manifest model, version
// derivation, the resolver, the rocks adapter and the component build
// backends, and materializes the project's .rocks/ tree.
//
// The cycle, in order: derive the version, run pre_build, gate the
// lock (resolve and rewrite unless --locked), materialize the pinned closure
// into .rocks/, run each component's build backend, lay the component files out
// under their install namespace, generate version.lua, then run post_build.
// tt package fetch is the same materialization step in isolation: strictly from
// the lock, without resolving or running backends.
//
// The build always works in project scope: <ProjectDir>/.rocks/. Selecting the
// product, choosing versions, deriving flags and archiving are neighbouring
// packages' jobs; this package only drives the cycle.
package build

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/build/backend"
	"github.com/tarantool/tt/cli/manifest/resolve"
	"github.com/tarantool/tt/cli/manifest/rocks"
	"github.com/tarantool/tt/cli/manifest/version"
)

// rocksDirName is the project-scope tree root the build materializes into.
const rocksDirName = ".rocks"

// Options configures one build (or fetch) run.
type Options struct {
	// ProjectDir is the directory holding app.manifest.toml; the build works in
	// its .rocks/ subtree. Required and must be absolute.
	ProjectDir string
	// Product selects the product to build; empty picks the default (or the
	// only) product.
	Product string
	// Component narrows the build to a single component of the product; empty
	// builds every component of the product.
	Component string
	// Locked gates the lock: a stale lock is a hard error instead of being
	// re-resolved and rewritten. Ignored by fetch.
	Locked bool
	// FetchOnly runs tt package fetch: materialize .rocks/ from the lock only,
	// skipping hooks, resolution, component backends, layout and version.lua.
	FetchOnly bool
	// TtVersion is the "tt x.y.z" string stamped into a freshly written lock's
	// generated_by field.
	TtVersion string
	// Tarantool carries the Tarantool facts the rocks adapter needs.
	Tarantool rocks.TarantoolInfo
	// Servers overrides the rock-server list; nil uses the adapter default.
	Servers []string
	// ShowOutput streams child (backend / hook) output when true.
	ShowOutput bool
	// Now stamps version.lua's built_at; the zero value uses the wall clock.
	Now time.Time
	// Logger receives the adapter's structured operation logs; nil disables it.
	Logger *slog.Logger
	// Warn receives non-fatal diagnostics (parse/validation/resolution
	// warnings). The library never logs directly; the CLI sets this to route
	// them through tt's logger. Nil drops them.
	Warn func(string)
}

// emit surfaces non-fatal diagnostics through the Warn sink, if any.
func (o Options) emit(warnings []string) {
	if o.Warn == nil {
		return
	}

	for _, w := range warnings {
		o.Warn(w)
	}
}

// builtAt returns the version.lua timestamp: Options.Now, or the wall clock when
// unset.
func (o Options) builtAt() time.Time {
	if o.Now.IsZero() {
		return time.Now()
	}

	return o.Now
}

// Run executes the build (or fetch) described by opts. It returns an *ExitError
// for the failures that carry a dedicated exit code (stale --locked,
// version.lua collision, backend failure) and a plain error otherwise;
// ExitCode maps either to a process exit code.
func Run(ctx context.Context, opts Options) error {
	man, err := readManifest(opts.ProjectDir, opts)
	if err != nil {
		return err
	}

	productName, product, err := selectProduct(man, opts.Product)
	if err != nil {
		return err
	}

	tree := filepath.Join(opts.ProjectDir, rocksDirName)
	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       tree,
		WorkingDir: opts.ProjectDir,
		Servers:    opts.Servers,
		Logger:     opts.Logger,
	}))

	if opts.FetchOnly {
		return runFetch(ctx, adapter, opts.ProjectDir, productName)
	}

	return runBuild(ctx, opts, man, adapter, tree, productName, product)
}

// readManifest reads, parses and validates app.manifest.toml from projectDir,
// surfacing parse and validation warnings through opts.
func readManifest(projectDir string, opts Options) (*manifest.Manifest, error) {
	path := filepath.Join(projectDir, manifestFileName)

	data, err := os.ReadFile(path) //nolint:gosec // Reads the caller's own manifest.
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", manifestFileName, err)
	}

	man, warnings, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", manifestFileName, err)
	}

	opts.emit(warnings)

	validationWarnings, err := man.Validate()
	if err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	opts.emit(validationWarnings)

	return man, nil
}

// runFetch materializes the product's locked closure into the tree without
// resolving or running backends — tt package fetch.
func runFetch(
	ctx context.Context, adapter *rocks.Adapter, projectDir, productName string,
) error {
	lock, err := loadLock(projectDir)
	if err != nil {
		return err
	}

	prod, ok := lock.Products[productName]
	if !ok {
		return exitErrorf(exitStateError,
			"lock has no closure for product %q; run tt package build", productName)
	}

	rockClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return fmt.Errorf("rocks client: %w", err)
	}

	return materialize(ctx, rockClient, projectDir, prod)
}

// runBuild runs the full build cycle.
func runBuild(
	ctx context.Context, opts Options, man *manifest.Manifest,
	adapter *rocks.Adapter, tree, productName string, product manifest.Product,
) error {
	components, err := selectComponents(product, opts.Component)
	if err != nil {
		return err
	}

	// Version is derived before pre_build so the hook sees TT_VERSION, and
	// before the tree is touched so a hook-generated file does not flip .dirty.
	// version.Derive spawns git internally and takes no context.
	ver, err := version.Derive(opts.ProjectDir) //nolint:contextcheck // Derive has no ctx.
	if err != nil {
		return fmt.Errorf("deriving version: %w", err)
	}

	ver.Flavor = man.Platform.Tarantool.EffectiveFlavor()

	preErr := runHook(ctx, man, ver, hookPreBuild, opts.ProjectDir, opts.ShowOutput)
	if preErr != nil {
		return exitErrorf(exitBackendError, "pre_build hook: %w", preErr)
	}

	engine := resolve.NewEngine(adapter, opts.ProjectDir, opts.TtVersion)

	lock, warnings, err := gateLock(ctx, engine, man, opts.ProjectDir, opts.Locked)
	if err != nil {
		return err
	}

	opts.emit(warnings)

	prod, ok := lock.Products[productName]
	if !ok {
		// A hash-fresh lock can still lack the product if it was hand-edited or
		// truncated (IsStale only checks the manifest and path-dep hashes).
		return exitErrorf(exitStateError, "lock has no closure for product %q", productName)
	}

	rockClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return fmt.Errorf("rocks client: %w", err)
	}

	matErr := materialize(ctx, rockClient, opts.ProjectDir, prod)
	if matErr != nil {
		return matErr
	}

	backendErr := runBackends(ctx, opts, man, adapter, tree, components, ver)
	if backendErr != nil {
		return backendErr
	}

	laidOut, err := layoutComponents(opts.ProjectDir, tree, man, components)
	if err != nil {
		return err
	}

	vluaErr := writeVersionLua(tree, man.Package.Name,
		man.Package.GenerateVersionLuaValue(), ver, opts.builtAt(), laidOut)
	if vluaErr != nil {
		return vluaErr
	}

	return runPostBuild(ctx, opts, man, ver)
}

// runBackends runs the build backend of every selected component that declares
// one, in the product's component order, placing artifacts in the component's
// lib namespace directory. A backend failure is exit code 2.
func runBackends(
	ctx context.Context, opts Options, man *manifest.Manifest,
	adapter *rocks.Adapter, tree string, components []string, ver version.Version,
) error {
	flags := adapter.Flags()

	for _, name := range components {
		component := man.Components[name]
		if component.Build == nil {
			continue
		}

		executor, err := backend.New(component.Build.Backend, flags, opts.ShowOutput)
		if err != nil {
			return fmt.Errorf("component %q: %w", name, err)
		}

		namespace := component.EffectiveNamespace(man.Package.Name)
		env := backend.Env{
			OutputDir:   componentOutputDir(tree, namespace),
			ProjectRoot: opts.ProjectDir,
			Package:     man.Package.Name,
			Component:   name,
			Version:     ver.SemVer,
			OS:          "",
			Arch:        "",
			Extra:       component.Build.Env,
		}

		cwd := backendCwd(opts.ProjectDir, component)

		runErr := executor.Run(ctx, *component.Build, cwd, env)
		if runErr != nil {
			return exitErrorf(exitBackendError, "building component %q: %w", name, runErr)
		}
	}

	return nil
}

// layoutComponents lays every selected component out under its namespace and
// returns the union of destination paths written, for the version.lua collision
// check.
func layoutComponents(
	projectDir, tree string, man *manifest.Manifest, components []string,
) ([]string, error) {
	var laidOut []string

	for _, name := range components {
		component := man.Components[name]
		compAbsPath := resolveDir(projectDir, component.Path)

		written, err := layoutComponent(tree, man.Package.Name, component, compAbsPath)
		if err != nil {
			return nil, err
		}

		laidOut = append(laidOut, written...)
	}

	return laidOut, nil
}

// runPostBuild runs the post_build hook after all components are built.
func runPostBuild(
	ctx context.Context, opts Options, man *manifest.Manifest, ver version.Version,
) error {
	err := runHook(ctx, man, ver, hookPostBuild, opts.ProjectDir, opts.ShowOutput)
	if err != nil {
		return exitErrorf(exitBackendError, "post_build hook: %w", err)
	}

	return nil
}

// componentOutputDir is a component's native artifact directory:
// <tree>/lib/tarantool/<namespace>/, flat when the namespace is empty. It
// matches TT_OUTPUT_DIR in the backend contract.
func componentOutputDir(tree, namespace string) string {
	if namespace == "" {
		return filepath.Join(tree, libTarantool)
	}

	return filepath.Join(tree, libTarantool, namespace)
}

// backendCwd is the working directory for a component's build: the build block's
// cwd override, or the component's path, resolved against the project root.
func backendCwd(projectDir string, component manifest.Component) string {
	if component.Build != nil && component.Build.Cwd != "" {
		return resolveDir(projectDir, component.Build.Cwd)
	}

	return resolveDir(projectDir, component.Path)
}
