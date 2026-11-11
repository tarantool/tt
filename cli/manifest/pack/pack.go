// Package pack implements tt package pack: it runs the manifest build and
// then assembles its result into a reproducible .tt archive (tar+zstd).
//
// Two packing modes exist. The default, with-deps, produces a self-contained
// archive carrying _runtime/ (Tarantool, tt and optionally TCM) and the full
// dependency closure in .rocks/, so installing it is a plain extraction with no
// network. The --without-deps mode drops both, leaving the package's own
// namespace subtrees and the programs its executable components built into
// .rocks/bin/; installing that archive extracts it and refetches the
// dependencies from the registry using the lock's pins.
//
// Neither mode carries the dev closure's rocks. The build pack runs first
// materializes [dev_dependencies] into .rocks/ like any other build, and so did
// every build the developer ran before it, so the archive is kept clean here -
// where its content is selected - rather than by declining to install them. See
// devOnlyRocks and stageRocks.
//
// The lock inside the archive still lists that closure. The archived lock is a
// record of the build, not an inventory of the archive - it also carries
// generated_by and the bundled runtime versions - and the dev versions belong
// to that record, since a generator run from the dev tree can shape what was
// packed. Nothing on the install side reads the section.
package pack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// Options configures one pack run. The build-facing fields mirror
// build.Options, which pack drives before archiving.
type Options struct {
	// ProjectDir is the directory holding app.manifest.toml. Required and must
	// be absolute.
	ProjectDir string
	// Product selects the product to pack; empty picks the default.
	Product string
	// Locked gates the lock: a stale lock is a hard error.
	Locked bool
	// WithoutDeps drops _runtime/ and foreign dependencies from the archive.
	WithoutDeps bool
	// Formats are what the pack makes of one build, FormatTt, FormatRock and
	// FormatRockSrc; none is the .tt archive alone.
	Formats []string
	// Revision is the LuaRocks revision of the rock, from 1; 0 is 1.
	Revision int
	// OutputDir overrides where the archive and the rock are written; empty
	// uses <ProjectDir>/_build/pack. A directory a rock is written to is
	// indexed as a rock server.
	OutputDir string
	// Build carries the remaining build knobs (Tarantool facts, servers, output
	// streaming, logger) through to build.RunResult.
	//
	// Pack owns some of these and overwrites whatever is set here: ProjectDir,
	// Product, Locked, Component and FetchOnly come from the fields above, and
	// Now is pinned by buildTime unless already set. Warn is taken over only
	// when Options.Warn is non-nil.
	Build build.Options
	// Runtime describes where the runtime components come from. Ignored when
	// WithoutDeps is set.
	Runtime RuntimeOptions
	// Signer, when set, signs the archive for the integrity checks: the
	// package and each runtime component it bundles get their hashes files
	// (signStage). A package packed WithoutDeps cannot be signed.
	Signer integrity.Signer
	// Warn receives non-fatal diagnostics; nil drops them.
	Warn func(string)
}

// Result reports what pack produced.
type Result struct {
	// Path is the absolute path of the archive; empty when the pack made
	// none.
	Path string
	// Rock, SrcRock and Rockspec are the absolute paths of the rock, of the
	// source rock and of their rockspec; empty when the pack made none.
	Rock     string
	SrcRock  string
	Rockspec string
	// SHA256 is the archive's checksum, lowercase hex.
	SHA256 string
	// Token is the platform token in the archive name.
	Token string
	// Bundled reports the runtime versions written into the lock; zero in
	// --without-deps mode.
	Bundled BundledVersions
}

// Run builds the project and packs it into what opts.Formats ask for: the
// .tt archive, the rock, or both, from the one build. It returns an
// *sdk.ExitError for failures carrying a dedicated exit code; cli/exitcode
// maps any error to a process exit code.
func Run(ctx context.Context, opts Options) (*Result, error) {
	err := checkFormats(opts)
	if err != nil {
		return nil, err
	}

	if opts.Signer != nil && opts.WithoutDeps {
		return nil, stateErrorf("%w", errSignedWithoutDeps)
	}

	built, err := runBuild(ctx, opts)
	if err != nil {
		return nil, err
	}

	result := &Result{}

	if opts.makes(FormatTt) {
		result, err = packTt(ctx, opts, built)
		if err != nil {
			return nil, err
		}
	}

	if opts.makes(FormatRock) || opts.makes(FormatRockSrc) {
		rocks, err := packRocks(ctx, opts, built)
		if err != nil {
			return nil, err
		}

		result.Rock = rocks.Rock
		result.SrcRock = rocks.SrcRock
		result.Rockspec = rocks.Rockspec
	}

	return result, nil
}

// packTt packs the build into the .tt archive.
func packTt(ctx context.Context, opts Options, built *build.Result) (*Result, error) {
	stageDir, cleanup, err := makeStageDir(opts)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	bundled, err := runtimeInto(stageDir, opts, built.Manifest)
	if err != nil {
		return nil, err
	}

	lockBytes, err := lockWithBundled(built.Lock, bundled)
	if err != nil {
		return nil, err
	}

	man := built.Manifest

	err = stage(ctx, stageDir, stageRequest{
		ProjectDir: opts.ProjectDir,
		Manifest:   man,
		LockBytes:  lockBytes,
		Version:    built.Version.SemVer,
		Tree:       built.Tree,
		WithDeps:   !opts.WithoutDeps,
		DevOnly:    devOnlyRocks(built.Lock),
		Warn:       opts.Warn,
	})
	if err != nil {
		return nil, err
	}

	natives, err := native.Files(stageDir)
	if err != nil {
		return nil, err
	}

	// The archive is labeled with the host platform as soon as it carries a
	// native file, so a file built for another one has to fail here rather
	// than on the machine it is installed on.
	err = checkNativeFiles(natives, native.Host())
	if err != nil {
		return nil, err
	}

	if opts.Signer != nil {
		err = signStage(stageDir, opts.ProjectDir, opts.Signer)
		if err != nil {
			return nil, err
		}
	}

	token := platformToken(!opts.WithoutDeps, len(natives) > 0)
	dest := filepath.Join(outputDir(opts),
		archiveName(man.Package.Name, built.Version.SemVer, token))

	sum, err := writeArchive(stageDir, dest)
	if err != nil {
		return nil, err
	}

	return &Result{
		Path: dest, Rock: "", SrcRock: "", Rockspec: "", SHA256: sum, Token: token,
		Bundled: bundled,
	}, nil
}

// runBuild drives the full build cycle pack is defined to include, so a clean
// tree needs no separate tt package build.
func runBuild(ctx context.Context, opts Options) (*build.Result, error) {
	buildOpts := opts.Build

	buildOpts.ProjectDir = opts.ProjectDir
	buildOpts.Product = opts.Product
	buildOpts.Locked = opts.Locked
	buildOpts.FetchOnly = false
	buildOpts.Component = ""

	// Only take over the build's warning sink when pack has one of its own;
	// otherwise a caller who configured Build.Warn and left Options.Warn nil
	// would lose every build diagnostic.
	if opts.Warn != nil {
		buildOpts.Warn = opts.Warn
	}

	// Pin version.lua's built_at, without which the same commit packs to a
	// different archive every time. A caller that set Now explicitly keeps it.
	if buildOpts.Now.IsZero() {
		buildOpts.Now = buildTime(ctx, opts.ProjectDir)
	}

	built, err := build.RunResult(ctx, buildOpts)
	if err != nil {
		// Build failures already carry their exit code (2 for a backend
		// failure); pass them through untouched.
		return nil, err
	}

	return built, nil
}

// makeStageDir creates the staging tree and returns a cleanup that removes it.
func makeStageDir(opts Options) (string, func(), error) {
	base := filepath.Join(opts.ProjectDir, buildDirName, packSubDir)

	err := os.MkdirAll(base, dirPerm)
	if err != nil {
		return "", nil, fmt.Errorf("creating %s: %w", base, err)
	}

	dir, err := os.MkdirTemp(base, "stage-")
	if err != nil {
		return "", nil, fmt.Errorf("creating staging directory: %w", err)
	}

	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// runtimeInto bundles _runtime/ unless the archive is --without-deps.
//
// The [platform] constraints are taken from the manifest here rather than from
// the caller: they are only known once the build has parsed it, and leaving the
// field to the CLI to fill made an unset Platform silently bundle nothing.
func runtimeInto(
	stageDir string, opts Options, man *manifest.Manifest,
) (BundledVersions, error) {
	if opts.WithoutDeps {
		return BundledVersions{}, nil
	}

	req := opts.Runtime

	req.Warn = opts.Warn
	req.Platform = man.Platform

	return bundleRuntime(stageDir, req)
}

// lockWithBundled stamps the bundled runtime versions into a copy of the lock
// and marshals it. In --without-deps mode the fields stay empty, which is what
// tells an installer the archive carries no runtime.
func lockWithBundled(lock *manifest.Lock, bundled BundledVersions) ([]byte, error) {
	if lock == nil {
		return nil, stateErrorf("build produced no lock")
	}

	// Lock.Marshal has a value receiver, so this copy leaves the build's lock
	// (and the on-disk file) untouched: bundled_* live only in the archive.
	stamped := *lock

	stamped.BundledTarantool = bundled.Tarantool
	stamped.BundledTt = bundled.Tt
	stamped.BundledTcm = bundled.Tcm

	out, err := stamped.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshaling lock: %w", err)
	}

	return out, nil
}

// outputDir picks where the archive lands.
func outputDir(opts Options) string {
	if opts.OutputDir != "" {
		return opts.OutputDir
	}

	return filepath.Join(opts.ProjectDir, buildDirName, packSubDir)
}

// devOnlyRocks lists the dev closure's rocks that no product closure holds -
// the ones the archive must not carry. A nil lock, or one with no dev closure,
// yields nothing and every rock of the tree is grafted into the archive.
//
// Not installing dev dependencies during a pack-driven build would not do this
// job: .rocks/ is the developer's standing tree, so a tt package build run an
// hour earlier has already put them there, and pack would stage them. The
// exclusion therefore belongs where the archive content is selected.
//
// "No product closure holds it" is deliberately checked against every product,
// not just the one being packed. A with-deps archive grafts every rock of the
// tree, which may incidentally carry another product's rocks; a rock that is dev in
// this reading and runtime in another product's is not a dev-only rock, and
// stripping it would under-pack a tree the archive promises to reproduce.
func devOnlyRocks(lock *manifest.Lock) []manifest.LockDependency {
	if lock == nil || len(lock.DevDependencies) == 0 {
		return nil
	}

	runtime := map[string]bool{}

	for _, product := range lock.Products {
		for _, dependency := range product.Dependencies {
			runtime[dependency.Name] = true
		}
	}

	var devOnly []manifest.LockDependency

	for _, dependency := range lock.DevDependencies {
		if !runtime[dependency.Name] {
			devOnly = append(devOnly, dependency)
		}
	}

	return devOnly
}
