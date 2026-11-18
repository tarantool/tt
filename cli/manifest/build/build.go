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
// a rock of .rocks/, then run post_build. What a backend makes is typed by
// where it goes - modules to lib/, data files to share/, programs to bin/ -
// and one component may make all three; what it made is checked and recorded
// by component and type (see results.go).
//
// The components are laid out in the package's stage, _build/rock/<product>/,
// as an unpacked binary rock - lua/, lib/ and bin/ - which is then installed
// into .rocks/ as the rock of the package, its other version replaced (see
// stage.go). The tree manifest so owns every file the build puts into the
// tree, and the stage keeps what the backends wrote for the next incremental
// build. A build for another platform than the machine tt runs on
// (Options.Target) keeps its stage and its records in
// _build/target/<os>-<arch>/ instead, and installs the rock of the package
// into a tree of its own there rather than into .rocks/ (targetStateDir).
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
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/projectlock"
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
	// Components narrow the build to these components of the product, with
	// Component, built in the order of the product; none, and no Component,
	// builds every component of the product.
	Components []string
	// Args are the arguments a command passes through to the build of the
	// one component Component and Components name, the Args of its Context
	// (sdkbuild.Context.Args), which its inputs hash too; with any, the run
	// is to name exactly one. A path dependency is built with none.
	Args []string
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
	// Jobs is the most jobs each build runs at once, the Jobs of its Context
	// (sdkbuild.Context.Jobs), as --jobs sets it; 0 leaves it to the
	// backends, one a CPU. The builds of the path dependencies get it too.
	// It is no input of an incremental build: a component is not built
	// again for another Jobs.
	Jobs int
	// Target is the platform the components of the package are built for,
	// as tt package build --target names it; the zero value is the machine
	// tt runs on (native.Host). A build for another platform fails before
	// anything is built for a component whose backend does not build for
	// another (sdkbuild.Backend.CheckTarget), keeps its state apart from
	// the builds for other platforms (targetStateDir) and installs the rock
	// of the package into a tree of its own there rather than into .rocks/,
	// which is the machine's: Result.Tree is that tree. The dependencies are
	// installed into .rocks/ and built for the machine, as for any build,
	// and so are the path dependencies and the source rocks: they are what
	// the builds run on the machine.
	Target native.Platform
	// Now stamps version.lua's built_at; the zero value uses the wall clock.
	Now time.Time
	// Logger receives the adapter's structured operation logs; nil disables it.
	Logger *slog.Logger
	// Warn receives non-fatal diagnostics (parse/validation/resolution
	// warnings). The library never logs directly; the CLI sets this to route
	// them through tt's logger. Nil drops them.
	Warn func(string)
	// Removed is told, a line each, of the rocks the run removes from the
	// tree once the lock's closure is in it: one the lock held when the tree
	// was last materialized and holds no more, and a version of a rock other
	// than the one the lock pins. The CLI prints them. Nil logs them at the
	// debug level instead.
	Removed func(string)
	// Check, when set, is given the manifest and the name of the product
	// selected, the default one for an empty Product, as soon as they are
	// known: before a hook, the resolution or a backend runs. Its error fails
	// the run as it is. tt package pack refuses there what it cannot pack,
	// which the build would otherwise take its whole time to get to.
	Check func(man *manifest.Manifest, product string) error
	// Backends are the build backends the manifests are validated with and
	// the components are built by; nil is those of the process
	// (buildreg.Process). A component of a backend none of them is fails the
	// build before anything is built, naming the dependency it is of.
	Backends *buildreg.Registry
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

// target returns the platform the components are built for: Target, or the
// machine tt runs on when it is not set.
func (o Options) target() native.Platform {
	if o.Target == (native.Platform{}) {
		return native.Host()
	}

	return o.Target
}

// cross reports whether the components are built for another platform than
// the machine tt runs on.
func (o Options) cross() bool {
	return o.target() != native.Host()
}

// backends returns the build backends of the run.
func (o Options) backends() *buildreg.Registry {
	if o.Backends == nil {
		return buildreg.Process()
	}

	return o.Backends
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
	// Target is the platform the components were built for: the machine tt
	// runs on, or Options.Target. A fetch run, and Prepare, report the
	// machine.
	Target native.Platform
	// Tree is the absolute path of the tree the rock of the package was
	// installed into: the materialized .rocks/ tree, or, for a build for
	// another platform, the tree of the target in the state of its build,
	// which holds the rock of the package alone (targetTree).
	Tree string
	// Stage is the absolute path of the stage of the product: its components
	// laid out as an unpacked binary rock (lua/, lib/, bin/), the rock of the
	// package was installed from. A fetch run lays out nothing and reports
	// none.
	Stage string
	// RocksLua is the absolute path of rocks.lua in Stage, the rocks the
	// application was built with; empty when the build generated none. The
	// rock tt package pack releases leaves it out: a project installing the
	// rock resolves rocks of its own.
	RocksLua string
	// Programs maps every component of the manifest whose build made
	// programs to those it put into the bin directory of Tree, by file name:
	// the package's own files there, as against the console scripts of its
	// dependencies. A component the run did not build is reported as its
	// last build recorded it, if at all. A fetch run reports none.
	Programs map[string][]string
	// Built are the components with a build block the run built, or found
	// up to date, by name: what each made, where the rock of the package
	// installed it. A fetch run reports none.
	Built map[string]Built
}

// Built is what the build of a component made, by type, as absolute paths in
// the tree the build installed the package into: the modules in
// lib/tarantool/<namespace>/, the data files in share/tarantool/<namespace>/
// and the programs in bin/ of .rocks.
type Built struct {
	Modules  []string
	Data     []string
	Programs []string
	// UpToDate reports an incremental build that was skipped: its inputs
	// were those of its last build, and what that build made was there.
	UpToDate bool
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
//
// The run holds the lock of the project (projectlock) from its start to its
// end, waiting for it while another tt holds it, unless ctx carries it
// already; the hooks and the backends run under it.
func RunResult(ctx context.Context, opts Options) (*Result, error) {
	ctx, release, err := lockProject(ctx, opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	defer release()

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

	if opts.FetchOnly {
		err = repairTree(ctx, adapter.Config(), opts.Warn)
		if err != nil {
			return nil, err
		}

		return runFetch(ctx, opts, adapter, man, tree, productName)
	}

	// The version is derived before the build writes anything: before the
	// tree is repaired, the lock gated and pre_build run, so that nothing tt
	// or a hook writes marks it dirty, and so that pre_build sees TT_VERSION.
	// The lock of the project, written before it, is in the build directory,
	// which the dirty check leaves out. version.Derive spawns git internally
	// and takes no context.
	ver, err := version.Derive(opts.ProjectDir) //nolint:contextcheck // Derive has no ctx.
	if err != nil {
		return nil, fmt.Errorf("deriving version: %w", err)
	}

	ver.Flavor = man.Platform.Tarantool.EffectiveFlavor()

	err = repairTree(ctx, adapter.Config(), opts.Warn)
	if err != nil {
		return nil, err
	}

	return runBuild(ctx, opts, man, adapter, tree, productName, product, ver)
}

// Prepare is RunResult with nothing built: it reads and checks the manifest,
// selects the product, runs opts.Check, gates the lock as a build does - a
// stale or missing one is resolved and written, and fails under opts.Locked -
// and derives the version. It installs nothing into the tree and runs no
// hook and no backend, so it needs no compiler and no headers of Tarantool:
// tt package pack --format rock-src, whose source rock holds the files of the
// project and no build output, packs from it. The result has no Tree, Stage
// or Programs. It holds the lock of the project as RunResult does.
func Prepare(ctx context.Context, opts Options) (*Result, error) {
	ctx, release, err := lockProject(ctx, opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	defer release()

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
		Manifest: man, Lock: lock, Version: ver, Product: productName, Target: native.Host(),
		Tree: "", Stage: "", RocksLua: "", Programs: nil,
	}, nil
}

// lockProject takes the lock of the project in projectDir for a run
// (projectlock.Acquire): the context that carries it, and its release.
func lockProject(ctx context.Context, projectDir string) (context.Context, func(), error) {
	locked, release, err := projectlock.Acquire(ctx, projectDir)
	if err != nil {
		return ctx, release, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	return locked, release, nil
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

	validationWarnings, err := man.Validate(opts.backends())
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

	return &Result{
		Manifest: man, Lock: lock, Product: productName, Target: native.Host(), Tree: tree,
	}, nil
}

// materializeLock materializes the closure of the product prod of lock and the
// lock's dev closure into the tree of adapter (materialize), then removes from
// the tree the rocks the lock no longer holds and the versions of a rock it
// does not pin (pruneTree). The rock of a path
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
		err = servers.addPathManifests(opts.ProjectDir, opts.backends(), prod.Dependencies,
			lock.DevDependencies)
	}

	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	manifests := newPathManifests(opts, adapter, prod.Dependencies, lock.DevDependencies)

	srcs, err := newSrcRocks(opts, adapter, manifests)
	if err != nil {
		return err
	}

	var before rockSet

	if opts.Tarantool.HeadersWarning != "" {
		before = installedRocks(ctx, adapter.Config().Tree)
	}

	err = materialize(ctx, srcInstaller{installer: registryClient, srcs: srcs}, pathClient,
		manifests, opts.ProjectDir, prod, lock.DevDependencies, servers)
	if err != nil {
		return err
	}

	opts.warnHeadersOfBuiltRocks(ctx, adapter.Config().Tree, before)

	return pruneTree(ctx, adapter.Config(), opts.ProjectDir, lock, nil,
		pruneReport{removed: opts.Removed}.report)
}

// runBuild runs the full build cycle.
func runBuild(
	ctx context.Context, opts Options, man *manifest.Manifest,
	adapter *rocks.Adapter, tree, productName string, product manifest.Product,
	ver version.Version,
) (*Result, error) {
	components, err := selectComponents(product, append(slices.Clone(opts.Components),
		opts.Component)...)
	if err != nil {
		return nil, err
	}

	if len(opts.Args) > 0 && len(components) != 1 {
		return nil, sdk.Errorf(sdk.ExitFailure, "%w: the arguments of a build go to the "+
			"build of one component, and the run builds %d", errArgsOfComponents,
			len(components))
	}

	proj, stage := targetProject(opts, man, productName)

	// The preflight comes before the lock is gated, to fail before a hook
	// and the resolution run: it reads the versions of a lock on disk that
	// is fresh, and none of another.
	//nolint:contextcheck // The hash of a path dependency asks git, which takes no context.
	proj.pinned = freshPins(opts.ProjectDir, man, productName)

	err = newBuilder(opts, proj, stage, version.Version{}).preflight(ctx, components)
	if err != nil {
		return nil, err
	}

	preErr := newBuilder(opts, proj, stage, ver).runHook(ctx, hookPreBuild)
	if preErr != nil {
		// runHook names the hook already.
		return nil, sdk.Errorf(sdk.ExitSystem, "%w", preErr)
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

	proj.pinned = pinnedClosure(prod.Dependencies, lock.DevDependencies)
	// rocks.lua of the package lists the closure the lock pins for the
	// product built, without the dev closure pinned holds too.
	proj.closure = prod.Dependencies

	built, err := buildComponents(ctx, opts, proj, stage, components, ver)
	if err != nil {
		return nil, err
	}

	rocksLua := ""
	if proj.rocksLua() {
		rocksLua = rocksLuaPath(stage, proj.versionLuaNamespace())
	}

	// The modules and the programs of a build for another platform do not
	// run on the machine, whose tree .rocks/ is: its rock goes into the tree
	// of its target.
	cfg := adapter.Config()
	if opts.cross() {
		cfg.Tree = targetTree(proj.stateDir)
		tree = cfg.Tree
	}

	err = installPackageRock(ctx, cfg, man, productName, prod, ver, stage)
	if err != nil {
		return nil, err
	}

	postErr := newBuilder(opts, proj, stage, ver).runHook(ctx, hookPostBuild)
	if postErr != nil {
		return nil, sdk.Errorf(sdk.ExitSystem, "%w", postErr)
	}

	return &Result{
		Manifest: man,
		Lock:     lock,
		Version:  ver,
		Product:  productName,
		Target:   opts.target(),
		Tree:     tree,
		Stage:    stage,
		RocksLua: rocksLua,
		Programs: built.programs,
		Built:    built.installed(proj, tree),
	}, nil
}

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
	// incremental components and of what each component made.
	stateDir string
	// man is the manifest of the project.
	man *manifest.Manifest
	// product is the product built.
	product string
	// pinned is the closure the lock of the build pins, which the backends
	// read the versions of rocks from (sdkbuild.Context.LockedVersion).
	// Before the lock is gated, during the preflight, it is the closure of
	// the lock on disk when that lock is fresh (freshPins), and empty
	// otherwise.
	pinned manifest.LockProduct
	// topLevel marks the build of the package itself (packageProject), as
	// against that of a path dependency or a source rock with a manifest:
	// only it writes rocks.lua, the closure of a dependency being the
	// consumer's to report.
	topLevel bool
	// closure is the closure of product as the lock pins it, which rocks.lua
	// of a top-level build lists; runBuild sets it once the lock is gated.
	closure []manifest.LockDependency
	// fromServer is set for the source rock of a rock server, unpacked: its
	// builds are given no mirror of the rock servers of the build
	// (sdkbuild.Context.Vendored), which tt package download does not fill
	// for it, and download what they need.
	fromServer bool
}

// packageProject is the project of the package itself, its state in its own
// _build/.
func packageProject(projectDir string, man *manifest.Manifest, product string) project {
	return project{
		dir: projectDir, stateDir: filepath.Join(projectDir, buildDirName), man: man,
		product: product, topLevel: true, closure: nil,
	}
}

// targetDirName is the directory of _build/ the builds of the package for
// other platforms than the machine tt runs on keep their state in, one
// directory a platform (targetStateDir).
const targetDirName = "target"

// targetTreeName is the directory of the state of a build for another
// platform the rock of the package is installed into (targetTree).
const targetTreeName = "tree"

// targetStateDir is the directory of the state of the builds of the package
// of the project at projectDir for target, another platform than the
// machine tt runs on: _build/target/<os>-<arch>/, with what _build/ holds
// for the builds for the machine - the stages, the records of what the
// components made and of the incremental builds, the TT_BUILD_DIR of every
// component, the libraries they made for linking, the target directory of
// cargo - so that neither build takes what the other made as its own, as
// up to date, nor removes it. The lock of the project, the builds of the
// path dependencies and the source rocks, which are built for the machine,
// and what tt package pack writes stay in _build/.
func targetStateDir(projectDir string, target native.Platform) string {
	return filepath.Join(projectDir, buildDirName, targetDirName, target.String())
}

// targetTree is the rocks tree in stateDir, the state of a build for
// another platform (targetStateDir), the rock of the package is installed
// into: .rocks/ is the tree of the machine, whose Tarantool and programs
// run what it holds. It holds the rock of the package alone, which tt
// package pack packs from it as it packs one from .rocks/.
func targetTree(stateDir string) string {
	return filepath.Join(stateDir, targetTreeName)
}

// targetProject returns the project of the package of a build of opts, and
// its stage: packageProject and packageStage for the machine tt runs on, and
// for another platform the same in the state of the target
// (targetStateDir).
func targetProject(opts Options, man *manifest.Manifest, product string) (project, string) {
	proj := packageProject(opts.ProjectDir, man, product)

	if !opts.cross() {
		return proj, packageStage(opts.ProjectDir, product)
	}

	proj.stateDir = targetStateDir(opts.ProjectDir, opts.target())

	return proj, filepath.Join(proj.stateDir, stageDirName, product)
}

// rocksLua reports whether a build of proj generates rocks.lua: a top-level
// build of a package that does not set generate_rocks_lua = false.
func (proj project) rocksLua() bool {
	return proj.topLevel && proj.man.Package.GenerateRocksLuaValue()
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
// the file checks of the modules (checkFiles), version.lua and, for a
// top-level build, rocks.lua. Then it removes from the stage what no
// component put there (settleStage). It returns what the components built
// (componentBuilds), from the record runBackends returns.
func buildComponents(
	ctx context.Context, opts Options, proj project, stage string, components []string,
	ver version.Version,
) (componentBuilds, error) {
	last, settle, err := openStage(opts, proj, stage, components)
	if err != nil {
		return componentBuilds{}, err
	}

	runs, err := newBuilder(opts, proj, stage, ver).runBackends(ctx, components, last)
	if err != nil {
		return componentBuilds{}, err
	}

	results, written := runs.results, runs.written
	builds := componentBuilds{
		programs: programsOf(results), results: results, built: runs.built,
		upToDate: runs.upToDate,
	}

	laidOut, err := layoutComponents(proj.dir, stage, proj.man, components, opts.backends())
	if err != nil {
		return componentBuilds{}, err
	}

	var allLaidOut []string

	for _, name := range components {
		allLaidOut = append(allLaidOut, laidOut[name]...)
	}

	checkWarnings, err := checkFiles(ctx, opts.backends().FileChecks(),
		checkedFiles(proj, stage, components, laidOut, results))
	if err != nil {
		return componentBuilds{}, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	opts.emit(checkWarnings)

	generate := proj.man.Package.GenerateVersionLuaValue()

	err = writeVersionLua(stage, proj.versionLuaNamespace(), generate, ver, opts.builtAt(),
		allLaidOut)
	if err != nil {
		return componentBuilds{}, err
	}

	if proj.rocksLua() {
		err = writeRocksLua(stage, proj.versionLuaNamespace(), version.RocksLua{
			Package: proj.man.Package.Name, Version: ver, Product: proj.product,
			Rocks: proj.closure,
		}, allLaidOut)
		if err != nil {
			return componentBuilds{}, err
		}
	}

	if !settle {
		return builds, nil
	}

	var generated []string

	if generate {
		generated = append(generated, versionLuaPath(stage, proj.versionLuaNamespace()))
	}

	if proj.rocksLua() {
		generated = append(generated, rocksLuaPath(stage, proj.versionLuaNamespace()))
	}

	err = settleStage(stage, proj, last, stageBuild{
		components: components, written: written, laidOut: laidOut, blocks: runs.blocks,
		generated: generated,
	})
	if err != nil {
		return componentBuilds{}, err
	}

	return builds, nil
}

// componentBuilds are what the component steps of a build did
// (buildComponents).
type componentBuilds struct {
	// programs are the programs of every component whose build made any,
	// by component, from the record.
	programs map[string][]string
	// results are the record: what the build of every component made.
	results map[string]madeFiles
	// built are the components the run built or found up to date, in the
	// order they were built.
	built []string
	// upToDate are those of built whose incremental build was skipped.
	upToDate map[string]bool
}

// installed returns what the components the run built made, where the
// rock of the package of proj installed it in tree (Result.Built).
func (b componentBuilds) installed(proj project, tree string) map[string]Built {
	installed := make(map[string]Built, len(b.built))

	for _, name := range b.built {
		namespace := proj.man.ComponentTarget(name).Namespace
		made := b.results[name]

		installed[name] = Built{
			Modules:  joinDir(filepath.Join(tree, "lib", "tarantool", namespace), made.Modules),
			Data:     joinDir(filepath.Join(tree, "share", "tarantool", namespace), made.Data),
			Programs: joinDir(filepath.Join(tree, "bin"), made.Programs),
			UpToDate: b.upToDate[name],
		}
	}

	return installed
}

// layoutComponents lays every selected component out under its namespace in
// the package's stage and returns the destination paths each one wrote, for
// the version.lua collision check and the record of the stage. A component
// whose sources are no files of the package has nothing to lay out but what
// its include names, and one of kind = "executable" nothing at all
// (sourceLayout): the default include would otherwise copy the Lua of, say,
// a test fixture of the sources of a Go program into the package's
// namespace. The layout of a component leaves out what its backend excludes.
func layoutComponents(
	projectDir, stage string, man *manifest.Manifest, components []string,
	backends *buildreg.Registry,
) (map[string][]string, error) {
	laidOut := map[string][]string{}

	for _, name := range components {
		component := man.Components[name]

		lays, exclude := sourceLayout(backends, component)
		if !lays {
			continue
		}

		compAbsPath := resolveDir(projectDir, component.Path)

		written, err := layoutComponent(stage, man.Package.Name, component, compAbsPath,
			exclude)
		if err != nil {
			return nil, err
		}

		laidOut[name] = written
	}

	return laidOut, nil
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

// componentShareDir is a component's data directory in the package's stage:
// <stage>/lua/<namespace>/, flat when the namespace is empty, which the
// install of the package's rock deploys to .rocks/share/tarantool/<namespace>/
// with the files the layout of the component takes (sdkbuild.Dirs.Share).
func componentShareDir(stage, namespace string) string {
	if namespace == "" {
		return filepath.Join(stage, rocktree.StageLua)
	}

	return filepath.Join(stage, rocktree.StageLua, namespace)
}

// componentDirName is the directory of _build/ the intermediate files of the
// builds of the components are in, one directory per component.
const componentDirName = "component"

// componentBuildDir is the directory the build of the component name keeps
// its intermediate files in between builds, TT_BUILD_DIR,
// _build/component/<name>/: the build tree of a cmake build, what a make or
// shell build makes on the way, out of the sources and kept, as the stage
// is, for the next build to rebuild only what changed. stateDir is the state
// directory of the project (project.stateDir, sdkbuild.Dirs.State): _build/
// of the package, and the one of a path dependency in _build/path/ of the
// project that builds it, so a dependency's build tree is not left in its
// directory. A backend keeps there what it shares between the components of
// the project: cargo its target directory.
func componentBuildDir(stateDir, name string) string {
	return filepath.Join(stateDir, componentDirName, name)
}

// linkDirName is the directory of _build/ the libraries the components make
// for linking are in (sdkbuild.Link), one directory per product.
const linkDirName = "link"

// componentLinkDir is where the build of the component name of the product
// puts what it makes for the components built after it to link,
// sdkbuild.Dirs.Link: _build/link/<product>/<name>/, in the state directory
// of the project (componentBuildDir), out of the stage and so of the
// package, and of the product, as the components a product builds before
// the one that links are those of the product.
func componentLinkDir(stateDir, product, name string) string {
	return filepath.Join(stateDir, linkDirName, product, name)
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
