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
	"slices"
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
	// Check, when set, is given the manifest and the name of the product
	// selected, the default one for an empty Product, as soon as they are
	// known: before a hook, the resolution or a backend runs. Its error fails
	// the run as it is. tt package pack refuses there what it cannot pack,
	// which the build would otherwise take its whole time to get to.
	Check func(man *manifest.Manifest, product string) error
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

	if opts.Check != nil {
		err = opts.Check(man, productName)
		if err != nil {
			return nil, err
		}
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
		return runFetch(ctx, opts, adapter, man, tree, productName)
	}

	return runBuild(ctx, opts, man, adapter, tree, productName, product)
}

// Prepare is RunResult with nothing built: it reads and checks the manifest,
// selects the product, runs opts.Check, gates the lock as a build does - a
// stale or missing one is resolved and written, and fails under opts.Locked -
// and derives the version. It installs nothing into the tree and runs no
// hook and no backend, so it needs no compiler and no headers of Tarantool:
// tt package pack --format rock-src, whose source rock holds the files of the
// project and no build output, packs from it. The result has no Tree, Stage
// or Executables.
func Prepare(ctx context.Context, opts Options) (*Result, error) {
	man, err := readManifest(opts.ProjectDir, opts)
	if err != nil {
		return nil, err
	}

	productName, _, err := selectProduct(man, opts.Product)
	if err != nil {
		return nil, err
	}

	if opts.Check != nil {
		err = opts.Check(man, productName)
		if err != nil {
			return nil, err
		}
	}

	registries, err := effectiveRegistries(opts, man)
	if err != nil {
		return nil, err
	}

	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       filepath.Join(opts.ProjectDir, rocksDirName),
		WorkingDir: opts.ProjectDir,
		Servers:    rocks.URLs(registries),
		Logger:     opts.Logger,
	}))

	ver, err := version.Derive(opts.ProjectDir) //nolint:contextcheck // Derive has no ctx.
	if err != nil {
		return nil, fmt.Errorf("deriving version: %w", err)
	}

	ver.Flavor = man.Platform.Tarantool.EffectiveFlavor()

	engine := resolve.NewEngine(adapter, opts.ProjectDir, opts.TtVersion)

	lock, warnings, err := gateLock(ctx, engine, man, opts.ProjectDir, opts.Locked)
	if err != nil {
		return nil, err
	}

	opts.emit(warnings)

	return &Result{
		Manifest: man, Lock: lock, Version: ver, Product: productName,
		Tree: "", Stage: "", Executables: nil,
	}, nil
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
// resolving or running backends — tt package fetch. The backends of a path
// dependency holding a manifest do run: such a dependency is built, as one
// shipping a rockspec is.
func runFetch(
	ctx context.Context, opts Options, adapter *rocks.Adapter,
	man *manifest.Manifest, tree, productName string,
) (*Result, error) {
	lock, err := loadLock(opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	prod, ok := lock.Products[productName]
	if !ok {
		return nil, sdk.Errorf(sdk.ExitFailure,
			"lock has no closure for product %q; run tt package build", productName)
	}

	err = materializeLock(ctx, opts, man, adapter, lock, prod)
	if err != nil {
		return nil, err
	}

	return &Result{Manifest: man, Lock: lock, Product: productName, Tree: tree}, nil
}

// materializeLock materializes the closure of the product prod of lock and the
// lock's dev closure into the tree of adapter (materialize), then removes from
// the tree what the lock does not pin (pruneTree). The rock of a path
// dependency holding a manifest declares its dependencies at the versions the
// lock pins.
func materializeLock(
	ctx context.Context, opts Options, man *manifest.Manifest, adapter *rocks.Adapter,
	lock *manifest.Lock, prod manifest.LockProduct,
) error {
	registryClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return fmt.Errorf("rocks registry client: %w", err)
	}

	// Path rocks are built by the native backend, whose typed Build operation
	// never resolves dependencies again; the lock remains the only closure.
	pathClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return fmt.Errorf("rocks path client: %w", err)
	}

	servers, err := dependencyServers(man, opts.ProjectDir, adapter.Config().Servers)
	if err == nil {
		err = servers.addPathManifests(opts.ProjectDir, prod.Dependencies, lock.DevDependencies)
	}

	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	manifests := newPathManifests(opts, adapter, prod.Dependencies, lock.DevDependencies)

	srcs, err := newSrcRocks(opts, adapter, manifests)
	if err != nil {
		return err
	}

	err = materialize(ctx, srcInstaller{installer: registryClient, srcs: srcs}, pathClient,
		manifests, opts.ProjectDir, prod, lock.DevDependencies, servers)
	if err != nil {
		return err
	}

	return pruneTree(ctx, adapter.Config(), opts.ProjectDir, lock, nil, logInfo(opts.Logger))
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

	proj := packageProject(opts.ProjectDir, man, productName)

	err = preflightBackends(proj, components, opts.Tarantool)
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

	err = materializeLock(ctx, opts, man, adapter, lock, prod)
	if err != nil {
		return nil, err
	}

	stage := packageStage(opts.ProjectDir, productName)

	programs, err := buildComponents(ctx, opts, proj, adapter, stage, components, ver)
	if err != nil {
		return nil, err
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

// preflightBackends checks what the go, cargo and cmake builds of the
// selected components of proj need before any of them runs
// (backend.Preflight), and that the c and lua-c ones have the headers of a
// Tarantool, info, to compile against: a missing toolchain then fails the
// build before it has resolved the closure and compiled the components ahead
// of it.
//
// A toolchain is looked for where the build looks for it: in the PATH of the
// component's [build].env, after .tarantool/bin of proj, the project being
// built - the package, or a path dependency in its own directory.
func preflightBackends(proj project, components []string, info rocks.TarantoolInfo) error {
	target := native.Host()

	for _, name := range components {
		component := proj.man.Components[name]
		if component.Build == nil {
			continue
		}

		err := backend.Preflight(*component.Build, backend.Env{
			ProjectRoot: proj.dir, OS: target.OS, Arch: target.Arch, Extra: component.Build.Env,
			Module: !component.IsExecutable(),
		})
		if err == nil {
			err = preflightHeaders(*component.Build, info)
		}

		if err != nil {
			return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
		}
	}

	return nil
}

// preflightHeaders checks that a c or lua-c build has a Tarantool to take
// the headers from: a tt with no Tarantool builds what needs none, and the
// cc driver would otherwise refuse it only once its turn came, saying
// nothing of why. A make, shell or cmake build is not told where the headers
// are then (TT_TARANTOOL_*), and fails on its own if it needs them.
func preflightHeaders(build manifest.Build, info rocks.TarantoolInfo) error {
	switch build.Backend {
	case backend.BackendC, backend.BackendLuaC:
	default:
		return nil
	}

	if info.IncludeDir() != "" {
		return nil
	}

	return fmt.Errorf("%w: %s", backend.ErrMissingHeaders, noTarantoolHint)
}

// noTarantoolHint says why a build has no headers of Tarantool, and how to
// give it some.
const noTarantoolHint = "no tarantool to take them from, neither in .tarantool/bin " +
	"of the project nor in PATH; tt binary install tarantool installs one"

// project is what the component steps of a build work on (buildComponents):
// the manifest of a project, its directory and the directory the build keeps
// its own state in. tt package build works on the package itself and its
// _build/; a path dependency holding a manifest is built by the same steps
// from its own directory, its state kept in the consumer's _build/ instead
// (pathbuild.go).
type project struct {
	// dir is the root of the project: its components, the working directory
	// of their builds, TT_PROJECT_ROOT and the root of the ignore rules of
	// their inputs.
	dir string
	// stateDir is the directory of the build's own state: the records of the
	// incremental components and of the programs of the executable ones.
	stateDir string
	// man is the manifest of the project.
	man *manifest.Manifest
	// product is the product built.
	product string
}

// packageProject is the project of the package itself, its state in its own
// _build/.
func packageProject(projectDir string, man *manifest.Manifest, product string) project {
	return project{
		dir: projectDir, stateDir: filepath.Join(projectDir, buildDirName), man: man,
		product: product,
	}
}

// versionLuaNamespace is the namespace version.lua of a build of proj goes
// under: the package name, but for a product released as a rock of another
// name, whose rock is to own no file of the rock of the package.
func (proj project) versionLuaNamespace() string {
	if rock := proj.man.ProductRock(proj.product); rock != "" {
		return rock
	}

	return proj.man.Package.Name
}

// buildComponents runs the component steps of a build of proj into stage:
// the backends of components (runBackends), their layout (layoutComponents),
// the check of the laid-out wasm files and version.lua. Then it removes from
// the stage what no component put there (settleStage). It returns the record
// of the programs of the executable components, as runBackends does.
func buildComponents(
	ctx context.Context, opts Options, proj project, adapter *rocks.Adapter,
	stage string, components []string, ver version.Version,
) (map[string][]string, error) {
	last, settle, err := openStage(opts, proj, stage, components)
	if err != nil {
		return nil, err
	}

	programs, written, err := runBackends(ctx, opts, proj, adapter, stage, components, ver, last)
	if err != nil {
		return nil, err
	}

	laidOut, err := layoutComponents(proj.dir, stage, proj.man, components)
	if err != nil {
		return nil, err
	}

	var allLaidOut []string

	for _, name := range components {
		allLaidOut = append(allLaidOut, laidOut[name]...)
	}

	wasmWarnings, err := checkWasmFiles(allLaidOut)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	opts.emit(wasmWarnings)

	generate := proj.man.Package.GenerateVersionLuaValue()

	err = writeVersionLua(stage, proj.versionLuaNamespace(), generate, ver, opts.builtAt(),
		allLaidOut)
	if err != nil {
		return nil, err
	}

	if !settle {
		return programs, nil
	}

	var generated []string

	if generate {
		generated = append(generated, versionLuaPath(stage, proj.versionLuaNamespace()))
	}

	err = settleStage(stage, proj, last, stageBuild{
		components: components, written: written, laidOut: laidOut, generated: generated,
	})
	if err != nil {
		return nil, err
	}

	return programs, nil
}

// runBackends runs the build backend of every selected component that declares
// one, in the product's component order, placing artifacts in the component's
// lib namespace directory of the package's stage, or, for an executable
// component, in its bin directory. A backend failure is exit code 2.
//
// It returns the record of the programs in the bin directory each executable
// component built, those of the components not built this time as an
// earlier build recorded them, and, for each component it built or skipped,
// the absolute paths of the files of the stage the backend left there: what
// a module component wrote into TT_OUTPUT_DIR (runModuleBackend), with what
// last, the record of the stage, says it wrote there before and is still
// there, and the programs of an executable one.
func runBackends(
	ctx context.Context, opts Options, proj project,
	adapter *rocks.Adapter, stage string, components []string, ver version.Version,
	last stageRecord,
) (map[string][]string, map[string][]string, error) {
	flags := adapter.Flags()
	target := native.Host()
	tarantoolPrefix, tarantoolIncludeDir := tarantoolDirs(opts.Tarantool)
	programs := readExecutables(proj.stateDir, opts.Warn)
	written := map[string][]string{}
	built := false

	for _, name := range components {
		component := proj.man.Components[name]
		if component.Build == nil {
			continue
		}

		executor, err := backend.New(component.Build.Backend, flags, opts.ShowOutput)
		if err != nil {
			return nil, nil, fmt.Errorf("component %q: %w", name, err)
		}

		outputDir := componentOutputDir(stage, component.EffectiveNamespace(proj.man.Package.Name))
		if component.IsExecutable() {
			outputDir = filepath.Join(stage, rocktree.StageBin)
		}

		env := backend.Env{
			OutputDir:           outputDir,
			ProjectRoot:         proj.dir,
			Package:             proj.man.Package.Name,
			Component:           name,
			Version:             ver.SemVer,
			OS:                  target.OS,
			Arch:                target.Arch,
			Extra:               component.Build.Env,
			Module:              !component.IsExecutable(),
			TarantoolPrefix:     tarantoolPrefix,
			TarantoolIncludeDir: tarantoolIncludeDir,
			BuildDir:            componentBuildDir(proj.stateDir, name),
			Fresh:               opts.Rebuild,
			CargoTargetDir:      filepath.Join(proj.stateDir, cargoDirName),
		}

		err = prepareBuildDir(env.BuildDir, opts.Rebuild)
		if err != nil {
			return nil, nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
		}

		cwd := backendCwd(proj.dir, component)

		if component.IsExecutable() {
			programs[name], err = runExecutableBackend(ctx, opts, proj, executor, name, component,
				cwd, env, programs[name])
			if err != nil {
				return nil, nil, err
			}

			written[name] = joinDir(outputDir, programs[name])
			built = true

			continue
		}

		recorded := inDir(outputDir, joinDir(stage, last.Components[name].Written))

		files, err := runModuleBackend(ctx, opts, proj, executor, name, component, cwd, env,
			recorded)
		if err != nil {
			return nil, nil, err
		}

		written[name] = joinDir(outputDir, files)
	}

	if !built {
		return keepExecutable(proj.man, programs), written, nil
	}

	programs, err := writeExecutables(proj.stateDir, proj.man, programs)
	if err != nil {
		return nil, nil, err
	}

	return programs, written, nil
}

// layoutComponents lays every selected component out under its namespace in
// the package's stage and returns the destination paths each one wrote, for
// the version.lua collision check and the record of the stage. An executable
// component has nothing to lay out: its build placed its programs already,
// and the default include would otherwise copy the Lua of, say, a test
// fixture of its sources into the package's namespace.
func layoutComponents(
	projectDir, stage string, man *manifest.Manifest, components []string,
) (map[string][]string, error) {
	laidOut := map[string][]string{}

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

		laidOut[name] = written
	}

	return laidOut, nil
}

// runModuleBackend runs the build of a module component and returns what it
// wrote into TT_OUTPUT_DIR, relative to it (runWritingBackend); recorded is
// what an earlier build of it wrote there. An incremental one is skipped when
// its inputs are those of its last build and what that build left is still
// there (see skipUnchanged), and then wrote what that build did; its build
// records what it wrote and the files of the component the layout takes.
// Those are the files of a static one the build checks (checkStaticModule),
// before the record is written: a module that fails the check is built
// again.
func runModuleBackend(
	ctx context.Context, opts Options, proj project, executor backend.Backend, name string,
	component manifest.Component, cwd string, env backend.Env, recorded []string,
) ([]string, error) {
	incremental := component.Build.IncrementalValue()
	static := component.Build.StaticValue()

	if !incremental && !static {
		return runWritingBackend(ctx, executor, name, component, cwd, env, recorded)
	}

	compAbsPath := resolveDir(proj.dir, component.Path)

	var state incrementalState

	if incremental {
		var (
			skipped bool
			err     error
		)

		state, skipped, err = skipUnchanged(opts, proj, name, component, compAbsPath, cwd, env)
		if err != nil {
			return nil, err
		}

		if skipped {
			return state.last.Written, nil
		}
	}

	written, err := runWritingBackend(ctx, executor, name, component, cwd, env,
		append(slices.Clone(recorded), state.last.Written...))
	if err != nil {
		return nil, err
	}

	laidOut, err := laidOutFiles(component, compAbsPath)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	if static {
		err = checkStaticModule(opts, name, moduleFiles(env.OutputDir, written,
			compAbsPath, laidOut))
		if err != nil {
			return nil, err
		}
	}

	if incremental {
		err = writeIncremental(proj.stateDir, name, incrementalRecord{
			Inputs:  state.inputs,
			Written: written,
			LaidOut: laidOut,
		})
		if err != nil {
			return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
		}
	}

	return written, nil
}

// runWritingBackend runs the backend of the component name and returns what
// it wrote into TT_OUTPUT_DIR, relative to it (writtenFiles): the files it
// created or changed, and those of recorded, which an earlier build wrote,
// that are still there - a make target that is up to date writes nothing.
func runWritingBackend(
	ctx context.Context, executor backend.Backend, name string, component manifest.Component,
	cwd string, env backend.Env, recorded []string,
) ([]string, error) {
	before, err := stampTree(env.OutputDir)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	err = runComponentBackend(ctx, executor, name, component, cwd, env)
	if err != nil {
		return nil, err
	}

	after, err := stampTree(env.OutputDir)
	if err != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	return writtenFiles(before, after, recorded), nil
}

// runExecutableBackend builds an executable component (see buildExecutable)
// and returns its programs. An incremental one is skipped as a module
// component is, its programs being what it wrote into TT_OUTPUT_DIR, and
// keeps the programs its last build recorded.
func runExecutableBackend(
	ctx context.Context, opts Options, proj project, executor backend.Backend, name string,
	component manifest.Component, cwd string, env backend.Env, recorded []string,
) ([]string, error) {
	if !component.Build.IncrementalValue() {
		return buildExecutable(ctx, executor, component, name, cwd, env, recorded)
	}

	compAbsPath := resolveDir(proj.dir, component.Path)

	state, skipped, err := skipUnchanged(opts, proj, name, component, compAbsPath, cwd, env)
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

	err = writeIncremental(proj.stateDir, name, incrementalRecord{
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
	opts Options, proj project, name string, component manifest.Component,
	compAbsPath, cwd string, env backend.Env,
) (incrementalState, bool, error) {
	var state incrementalState

	inputs, err := componentInputs(proj.dir, compAbsPath, *component.Build)
	if err != nil {
		return state, false, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
	}

	state.inputs = inputs

	last, found := readIncremental(proj.stateDir, name)
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

const (
	// componentDirName is the directory of _build/ the intermediate files of
	// the builds of the components are in, one directory per component.
	componentDirName = "component"
	// cargoDirName is the directory of _build/ the cargo builds of a project
	// share as their target directory.
	cargoDirName = "cargo"
)

// componentBuildDir is the directory the build of the component name keeps
// its intermediate files in between builds, TT_BUILD_DIR,
// _build/component/<name>/: the build tree of a cmake build, what a make or
// shell build makes on the way, out of the sources and kept, as the stage
// is, for the next build to rebuild only what changed. stateDir is the state
// directory of the project (project.stateDir): _build/ of the package, and
// the one of a path dependency in _build/path/ of the project that builds
// it, so a dependency's build tree is not left in its directory.
//
// The cargo builds of a project do not build there, but in one target
// directory, _build/cargo/ of stateDir, as cargo builds the packages of a
// workspace in one: a module and a program of one crate, or of one
// workspace, share the dependencies compiled once. Cargo keeps what it
// builds of each package and profile apart in it, and locks it while it
// builds; a binary or a library of one name two workspaces put at one path
// is taken from there right after its build, by the path cargo reports.
func componentBuildDir(stateDir, name string) string {
	return filepath.Join(stateDir, componentDirName, name)
}

// prepareBuildDir makes the directory of the intermediate files of a build,
// TT_BUILD_DIR, ready for it to write into at once: emptied first when the
// build is from scratch (fresh, tt package build --rebuild), so that what an
// earlier build made, with another compiler say, is made again, and created.
func prepareBuildDir(dir string, fresh bool) error {
	if fresh {
		err := os.RemoveAll(dir)
		if err != nil {
			return fmt.Errorf("emptying %s: %w", dir, err)
		}
	}

	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	return nil
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
