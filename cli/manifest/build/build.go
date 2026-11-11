// Package build is the orchestration layer of the tt manifest pipeline: it runs
// the full build cycle of a package (tt package build) and its fetch-only slice
// (tt package fetch). It stitches together the manifest model, version
// derivation, the resolver, the rocks adapter and the component build
// backends, and materializes the project's .rocks/ tree.
//
// The cycle, in order: derive the version, run pre_build, gate the
// lock (resolve and rewrite unless --locked), materialize the pinned closure
// into .rocks/, run each component's build backend, lay the component files out
// under their install namespace, generate version.lua, install the package as
// a rock of .rocks/, then run post_build. An executable component builds
// programs into the bin directory of the package instead, and lays nothing
// out; what it built is checked and recorded (see executables.go).
//
// The components are laid out in the package's stage, _build/rock/<product>/,
// as an unpacked binary rock - lua/, lib/ and bin/ - which is then installed
// into .rocks/ as the rock of the package, its other version replaced (see
// stage.go). The tree manifest so owns every file the build puts into the
// tree, and the stage keeps what the backends wrote for the next incremental
// build.
// tt package fetch is the same materialization step in isolation: strictly from
// the lock, without resolving or running backends.
//
// Both operate on a developer's project, so both materialize the lock's dev
// closure into the same .rocks/ tree alongside the product's: [dev_dependencies]
// exists to be requirable while developing and testing. Nothing here keeps them
// out of a package archive - that is cli/manifest/pack's exclusion, applied
// where the archive content is selected, because .rocks/ is a standing tree and
// a build run an hour ago has already put dev rocks in it.
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

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build/backend"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
	"github.com/tarantool/tt/v3/cli/manifest/version"
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
	// Registries carries the rock servers the caller collected from the
	// command line and the environment. The manifest's own list and the
	// project directory are filled in here, so the caller need not parse the
	// manifest to know which servers a build will query.
	Registries rocks.Sources
	// ShowOutput streams child (backend / hook) output when true.
	ShowOutput bool
	// Rebuild builds every incremental component, whatever its last build
	// recorded: for a change the inputs do not cover, such as a new version
	// of a compiler. The build is recorded as ever.
	Rebuild bool
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

// Result reports what a build produced. Callers that only drive the build for
// its side effects ignore it; tt package pack consumes it to assemble the
// archive without re-reading and re-deriving the same state from disk.
type Result struct {
	// Manifest is the parsed and validated app.manifest.toml.
	Manifest *manifest.Manifest
	// Lock is the lock the build resolved against, already gated. Its
	// bundled_*_version fields are whatever was on disk; pack fills them.
	Lock *manifest.Lock
	// Version is the derived package version, with Flavor set from
	// [platform].tarantool.
	Version version.Version
	// Product is the name of the product that was built, with the default
	// already selected.
	Product string
	// Tree is the absolute path of the materialized .rocks/ tree.
	Tree string
	// Stage is the absolute path of the stage of the product: its components
	// laid out as an unpacked binary rock (lua/, lib/, bin/), the rock of the
	// package was installed from. A fetch run lays out nothing and reports
	// none.
	Stage string
	// Executables maps every executable component of the manifest to the
	// programs its build put into the bin directory of Tree, by file name:
	// the package's own files there, as against the console scripts of its
	// dependencies. A component the run did not build is reported as its
	// last build recorded it, if at all. A fetch run reports none.
	Executables map[string][]string
}

// Run executes the build (or fetch) described by opts, discarding the result.
// It returns an *sdk.ExitError for the failures that carry a dedicated exit
// code (stale --locked, version.lua collision, backend failure) and a plain
// error otherwise; cli/exitcode maps either to a process exit code.
func Run(ctx context.Context, opts Options) error {
	_, err := RunResult(ctx, opts)

	return err
}

// RunResult is Run, additionally reporting the manifest, lock, version and tree
// the build worked with. A fetch run reports the manifest, lock and tree; it
// derives no version and runs no backends.
func RunResult(ctx context.Context, opts Options) (*Result, error) {
	man, err := readManifest(opts.ProjectDir, opts)
	if err != nil {
		return nil, err
	}

	productName, product, err := selectProduct(man, opts.Product)
	if err != nil {
		return nil, err
	}

	registries, err := effectiveRegistries(opts, man)
	if err != nil {
		return nil, err
	}

	tree := filepath.Join(opts.ProjectDir, rocksDirName)
	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       tree,
		WorkingDir: opts.ProjectDir,
		Servers:    rocks.URLs(registries),
		Logger:     opts.Logger,
	}))

	err = repairTree(ctx, adapter.Config(), opts.Warn)
	if err != nil {
		return nil, err
	}

	if opts.FetchOnly {
		return runFetch(ctx, adapter, opts.ProjectDir, man, tree, productName, opts.Logger)
	}

	return runBuild(ctx, opts, man, adapter, tree, productName, product)
}

// effectiveRegistries settles the rock-server list for this run: the caller's
// command-line and environment lists over the manifest's own, over the
// built-in defaults.
func effectiveRegistries(opts Options, man *manifest.Manifest) ([]rocks.Registry, error) {
	sources := opts.Registries

	sources.Manifest = man.Platform.Registries
	sources.ProjectDir = opts.ProjectDir

	if sources.WorkingDir == "" {
		sources.WorkingDir = opts.ProjectDir
	}

	registries, err := rocks.EffectiveRegistries(sources)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	return registries, nil
}

// readManifest reads, parses and validates app.manifest.toml from projectDir,
// surfacing parse and validation warnings through opts.
func readManifest(projectDir string, opts Options) (*manifest.Manifest, error) {
	path := filepath.Join(projectDir, manifestFileName)

	data, err := os.ReadFile(path)
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
	ctx context.Context, adapter *rocks.Adapter, projectDir string,
	man *manifest.Manifest, tree, productName string, logger *slog.Logger,
) (*Result, error) {
	lock, err := loadLock(projectDir)
	if err != nil {
		return nil, err
	}

	prod, ok := lock.Products[productName]
	if !ok {
		return nil, sdk.Errorf(sdk.ExitFailure,
			"lock has no closure for product %q; run tt package build", productName)
	}

	registryClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return nil, fmt.Errorf("rocks registry client: %w", err)
	}

	// Path rocks are built by the native backend, whose typed Build operation
	// never resolves dependencies again; the lock remains the only closure.
	pathClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return nil, fmt.Errorf("rocks path client: %w", err)
	}

	servers, err := dependencyServers(man, projectDir, adapter.Config().Servers)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	matErr := materialize(ctx, registryClient, pathClient, projectDir, prod,
		lock.DevDependencies, servers)
	if matErr != nil {
		return nil, matErr
	}

	err = pruneTree(ctx, adapter.Config(), projectDir, lock, nil, logInfo(logger))
	if err != nil {
		return nil, err
	}

	return &Result{Manifest: man, Lock: lock, Product: productName, Tree: tree}, nil
}

// runBuild runs the full build cycle.
func runBuild(
	ctx context.Context, opts Options, man *manifest.Manifest,
	adapter *rocks.Adapter, tree, productName string, product manifest.Product,
) (*Result, error) {
	components, err := selectComponents(product, opts.Component)
	if err != nil {
		return nil, err
	}

	err = preflightBackends(man, components)
	if err != nil {
		return nil, err
	}

	// Version is derived before pre_build so the hook sees TT_VERSION, and
	// before the tree is touched so a hook-generated file does not flip .dirty.
	// version.Derive spawns git internally and takes no context.
	ver, err := version.Derive(opts.ProjectDir) //nolint:contextcheck // Derive has no ctx.
	if err != nil {
		return nil, fmt.Errorf("deriving version: %w", err)
	}

	ver.Flavor = man.Platform.Tarantool.EffectiveFlavor()

	preErr := runHook(ctx, opts, man, ver, hookPreBuild)
	if preErr != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "pre_build hook: %w", preErr)
	}

	engine := resolve.NewEngine(adapter, opts.ProjectDir, opts.TtVersion)

	lock, warnings, err := gateLock(ctx, engine, man, opts.ProjectDir, opts.Locked)
	if err != nil {
		return nil, err
	}

	opts.emit(warnings)

	prod, ok := lock.Products[productName]
	if !ok {
		// A hash-fresh lock can still lack the product if it was hand-edited or
		// truncated (IsStale only checks the manifest and path-dep hashes).
		return nil, sdk.Errorf(sdk.ExitFailure,
			"lock has no closure for product %q", productName)
	}

	registryClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return nil, fmt.Errorf("rocks registry client: %w", err)
	}

	pathClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return nil, fmt.Errorf("rocks path client: %w", err)
	}

	servers, err := dependencyServers(man, opts.ProjectDir, adapter.Config().Servers)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	matErr := materialize(ctx, registryClient, pathClient, opts.ProjectDir, prod,
		lock.DevDependencies, servers)
	if matErr != nil {
		return nil, matErr
	}

	err = pruneTree(ctx, adapter.Config(), opts.ProjectDir, lock, nil, logInfo(opts.Logger))
	if err != nil {
		return nil, err
	}

	stage := packageStage(opts.ProjectDir, productName)

	programs, err := runBackends(ctx, opts, man, adapter, stage, components, ver)
	if err != nil {
		return nil, err
	}

	laidOut, err := layoutComponents(opts.ProjectDir, stage, man, components)
	if err != nil {
		return nil, err
	}

	wasmWarnings, err := checkWasmFiles(laidOut)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	opts.emit(wasmWarnings)

	vluaErr := writeVersionLua(stage, man.Package.Name,
		man.Package.GenerateVersionLuaValue(), ver, opts.builtAt(), laidOut)
	if vluaErr != nil {
		return nil, vluaErr
	}

	err = installPackageRock(ctx, adapter.Config(), man, productName, prod, ver, stage)
	if err != nil {
		return nil, err
	}

	postErr := runPostBuild(ctx, opts, man, ver)
	if postErr != nil {
		return nil, postErr
	}

	return &Result{
		Manifest:    man,
		Lock:        lock,
		Version:     ver,
		Product:     productName,
		Tree:        tree,
		Stage:       stage,
		Executables: programs,
	}, nil
}

// preflightBackends checks what the go, cargo and cmake builds of the selected
// components need before any of them runs (backend.Preflight): a missing
// toolchain then fails the build before it has resolved the closure and
// compiled the components ahead of it.
func preflightBackends(man *manifest.Manifest, components []string) error {
	target := native.Host()

	for _, name := range components {
		component := man.Components[name]
		if component.Build == nil {
			continue
		}

		err := backend.Preflight(*component.Build, backend.Env{OS: target.OS, Arch: target.Arch})
		if err != nil {
			return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
		}
	}

	return nil
}

// runBackends runs the build backend of every selected component that declares
// one, in the product's component order, placing artifacts in the component's
// lib namespace directory of the package's stage, or, for an executable
// component, in its bin directory. A backend failure is exit code 2.
//
// It returns the record of the programs in the bin directory each executable
// component built, those of the components not built this time as an
// earlier build recorded them.
func runBackends(
	ctx context.Context, opts Options, man *manifest.Manifest,
	adapter *rocks.Adapter, stage string, components []string, ver version.Version,
) (map[string][]string, error) {
	flags := adapter.Flags()
	target := native.Host()
	tarantoolPrefix, tarantoolIncludeDir := tarantoolDirs(opts.Tarantool)
	programs := readExecutables(opts.ProjectDir, opts.Warn)
	built := false

	for _, name := range components {
		component := man.Components[name]
		if component.Build == nil {
			continue
		}

		executor, err := backend.New(component.Build.Backend, flags, opts.ShowOutput)
		if err != nil {
			return nil, fmt.Errorf("component %q: %w", name, err)
		}

		outputDir := componentOutputDir(stage, component.EffectiveNamespace(man.Package.Name))
		if component.IsExecutable() {
			outputDir = filepath.Join(stage, rocktree.StageBin)
		}

		env := backend.Env{
			OutputDir:           outputDir,
			ProjectRoot:         opts.ProjectDir,
			Package:             man.Package.Name,
			Component:           name,
			Version:             ver.SemVer,
			OS:                  target.OS,
			Arch:                target.Arch,
			Extra:               component.Build.Env,
			Module:              !component.IsExecutable(),
			TarantoolPrefix:     tarantoolPrefix,
			TarantoolIncludeDir: tarantoolIncludeDir,
			BuildDir:            componentBuildDir(opts.ProjectDir, name),
			Fresh:               opts.Rebuild,
		}

		cwd := backendCwd(opts.ProjectDir, component)

		if component.IsExecutable() {
			programs[name], err = runExecutableBackend(ctx, opts, executor, name, component,
				cwd, env, programs[name])
			if err != nil {
				return nil, err
			}

			built = true

			continue
		}

		err = runModuleBackend(ctx, opts, executor, name, component, cwd, env)
		if err != nil {
			return nil, err
		}
	}

	if !built {
		return keepExecutable(man, programs), nil
	}

	return writeExecutables(opts.ProjectDir, man, programs)
}

// layoutComponents lays every selected component out under its namespace in
// the package's stage and returns the union of destination paths written, for
// the version.lua collision check. An executable component has nothing to lay
// out: its build placed its programs already, and the default include would
// otherwise copy the Lua of, say, a test fixture of its sources into the
// package's namespace.
func layoutComponents(
	projectDir, stage string, man *manifest.Manifest, components []string,
) ([]string, error) {
	var laidOut []string

	for _, name := range components {
		component := man.Components[name]
		if component.IsExecutable() {
			continue
		}

		compAbsPath := resolveDir(projectDir, component.Path)

		written, err := layoutComponent(stage, man.Package.Name, component, compAbsPath)
		if err != nil {
			return nil, err
		}

		laidOut = append(laidOut, written...)
	}

	return laidOut, nil
}

// runModuleBackend runs the build of a module component. An incremental one
// is skipped when its inputs are those of its last build and what that build
// left is still there (see skipUnchanged); its build records what it wrote
// into TT_OUTPUT_DIR and the files of the component the layout takes.
func runModuleBackend(
	ctx context.Context, opts Options, executor backend.Backend, name string,
	component manifest.Component, cwd string, env backend.Env,
) error {
	if !component.Build.IncrementalValue() {
		return runComponentBackend(ctx, executor, name, component, cwd, env)
	}

	compAbsPath := resolveDir(opts.ProjectDir, component.Path)

	state, skipped, err := skipUnchanged(opts, name, component, compAbsPath, cwd, env)
	if err != nil || skipped {
		return err
	}

	before, err := stampTree(env.OutputDir)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	err = runComponentBackend(ctx, executor, name, component, cwd, env)
	if err != nil {
		return err
	}

	after, err := stampTree(env.OutputDir)
	if err == nil {
		var laidOut []string

		laidOut, err = laidOutFiles(component, compAbsPath)
		if err == nil {
			err = writeIncremental(opts.ProjectDir, name, incrementalRecord{
				Inputs:  state.inputs,
				Written: writtenFiles(before, after, state.last.Written),
				LaidOut: laidOut,
			})
		}
	}

	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	return nil
}

// runExecutableBackend builds an executable component (see buildExecutable)
// and returns its programs. An incremental one is skipped as a module
// component is, its programs being what it wrote into TT_OUTPUT_DIR, and
// keeps the programs its last build recorded.
func runExecutableBackend(
	ctx context.Context, opts Options, executor backend.Backend, name string,
	component manifest.Component, cwd string, env backend.Env, recorded []string,
) ([]string, error) {
	if !component.Build.IncrementalValue() {
		return buildExecutable(ctx, executor, component, name, cwd, env, recorded)
	}

	compAbsPath := resolveDir(opts.ProjectDir, component.Path)

	state, skipped, err := skipUnchanged(opts, name, component, compAbsPath, cwd, env)
	if err != nil {
		return nil, err
	}

	if skipped {
		return state.last.Written, nil
	}

	programs, err := buildExecutable(ctx, executor, component, name, cwd, env, recorded)
	if err != nil {
		return nil, err
	}

	err = writeIncremental(opts.ProjectDir, name, incrementalRecord{
		Inputs:  state.inputs,
		Written: programs,
		LaidOut: nil,
	})
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	return programs, nil
}

// runComponentBackend runs the backend of the component name.
func runComponentBackend(
	ctx context.Context, executor backend.Backend, name string, component manifest.Component,
	cwd string, env backend.Env,
) error {
	err := executor.Run(ctx, *component.Build, cwd, env)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	return nil
}

// skipUnchanged hashes the inputs of an incremental component and reports
// whether its build can be skipped: they are those of its last build, and
// what that build left is still there - the files its output lists, the
// files of the component the layout took and the files it wrote into
// TT_OUTPUT_DIR. The files output lists are copied into TT_OUTPUT_DIR before
// the last are looked for, as the build itself copies them: the tree may have
// been cleared since.
func skipUnchanged(
	opts Options, name string, component manifest.Component, compAbsPath, cwd string,
	env backend.Env,
) (incrementalState, bool, error) {
	var state incrementalState

	inputs, err := componentInputs(opts.ProjectDir, compAbsPath, *component.Build)
	if err != nil {
		return state, false, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	state.inputs = inputs

	last, found := readIncremental(opts.ProjectDir, name)
	if found {
		state.last = last
	}

	if opts.Rebuild || !found || last.Inputs != inputs ||
		!allPresent(cwd, component.Build.Output) || !allPresent(compAbsPath, last.LaidOut) {
		return state, false, nil
	}

	if len(component.Build.Output) > 0 {
		err = backend.CopyOutputs(env.OutputDir, cwd, component.Build.Output)
		if err != nil {
			return state, false, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
		}
	}

	if !allPresent(env.OutputDir, last.Written) {
		return state, false, nil
	}

	if opts.Logger != nil {
		opts.Logger.Info("component is up to date", "component", name)
	}

	return state, true, nil
}

// runPostBuild runs the post_build hook after all components are built.
func runPostBuild(
	ctx context.Context, opts Options, man *manifest.Manifest, ver version.Version,
) error {
	err := runHook(ctx, opts, man, ver, hookPostBuild)
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "post_build hook: %w", err)
	}

	return nil
}

// componentOutputDir is a component's native artifact directory in the
// package's stage: <stage>/lib/<namespace>/, flat when the namespace is
// empty, which the install of the package's rock deploys to
// .rocks/lib/tarantool/<namespace>/. It matches TT_OUTPUT_DIR in the backend
// contract.
func componentOutputDir(stage, namespace string) string {
	if namespace == "" {
		return filepath.Join(stage, rocktree.StageLib)
	}

	return filepath.Join(stage, rocktree.StageLib, namespace)
}

// cmakeDirName is the directory of _build/ the build trees of the cmake
// components are in, one per component.
const cmakeDirName = "cmake"

// componentBuildDir is the directory the backend of the component name keeps
// its state in between builds, _build/cmake/<name>/: the build tree of a
// cmake build, out of the source and kept, as the stage is, for the next
// build to rebuild only what changed. The other backends keep none.
func componentBuildDir(projectDir, name string) string {
	return filepath.Join(projectDir, buildDirName, cmakeDirName, name)
}

// tarantoolDirs is where the Tarantool the build is for is, as the build
// tells a backend: its install prefix, when tt knows it, and the directory
// of its C headers under the prefix, the include directory of the c and
// lua-c backends, when module.h is there. Either is empty otherwise.
func tarantoolDirs(info rocks.TarantoolInfo) (string, string) {
	if info.Prefix == "" {
		return "", ""
	}

	includeDir := info.IncludeDir()

	_, err := os.Stat(filepath.Join(includeDir, "module.h"))
	if err != nil {
		return info.Prefix, ""
	}

	return info.Prefix, includeDir
}

// backendCwd is the working directory for a component's build: the build block's
// cwd override, or the component's path, resolved against the project root.
func backendCwd(projectDir string, component manifest.Component) string {
	if component.Build != nil && component.Build.Cwd != "" {
		return resolveDir(projectDir, component.Build.Cwd)
	}

	return resolveDir(projectDir, component.Path)
}
