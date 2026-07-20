// Package pack implements tt package pack: it runs the manifest build and
// then assembles its result into a reproducible .tt archive (tar+zstd).
//
// Two packing modes exist. The default, with-deps, produces a self-contained
// archive carrying _runtime/ (Tarantool, tt and optionally TCM) and the full
// dependency closure in .rocks/, so installing it is a plain extraction with no
// network. The --without-deps mode drops both, leaving the package's own
// namespace subtrees; installing that archive extracts it and refetches the
// dependencies from the registry using the lock's pins.
package pack

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/build"
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
	// OutputDir overrides where the archive is written; empty uses
	// <ProjectDir>/_build/pack.
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
	// Warn receives non-fatal diagnostics; nil drops them.
	Warn func(string)
}

// Result reports what pack produced.
type Result struct {
	// Path is the absolute path of the archive.
	Path string
	// SHA256 is the archive's checksum, lowercase hex.
	SHA256 string
	// Token is the platform token in the archive name.
	Token string
	// Bundled reports the runtime versions written into the lock; zero in
	// --without-deps mode.
	Bundled BundledVersions
}

// Run builds the project and packs it into an archive. It returns an
// *build.ExitError for failures carrying a dedicated exit code; ExitCode maps
// any error to a process exit code.
func Run(ctx context.Context, opts Options) (*Result, error) {
	built, err := runBuild(ctx, opts)
	if err != nil {
		return nil, err
	}

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
	namespaces, hasFlat := packageNamespaces(man, built.Product)

	if err := stage(stageDir, stageRequest{
		ProjectDir:       opts.ProjectDir,
		Manifest:         man,
		LockBytes:        lockBytes,
		Version:          built.Version.SemVer,
		Tree:             built.Tree,
		WithDeps:         !opts.WithoutDeps,
		Namespaces:       namespaces,
		HasFlatNamespace: hasFlat,
	}); err != nil {
		return nil, err
	}

	native, err := hasNativeArtifacts(stageDir)
	if err != nil {
		return nil, err
	}

	token := platformToken(!opts.WithoutDeps, native)
	dest := filepath.Join(outputDir(opts),
		archiveName(man.Package.Name, built.Version.SemVer, token))

	sum, err := writeArchive(stageDir, dest)
	if err != nil {
		return nil, err
	}

	return &Result{Path: dest, SHA256: sum, Token: token, Bundled: bundled}, nil
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
	if err := os.MkdirAll(base, dirPerm); err != nil {
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

// packageNamespaces lists the rocks-tree namespaces the package itself owns,
// which is what --without-deps keeps. Every component of the product
// contributes its effective namespace.
func packageNamespaces(man *manifest.Manifest, productName string) ([]string, bool) {
	seen := map[string]bool{}

	var namespaces []string

	add := func(ns string) {
		if seen[ns] {
			return
		}

		seen[ns] = true
		namespaces = append(namespaces, ns)
	}

	// The package name is always owned: version.lua is generated under it even
	// when no component lays anything there.
	add(man.Package.Name)

	product, ok := man.Products[productName]
	if !ok {
		return namespaces, false
	}

	flat := false

	for _, name := range product.Components {
		component, ok := man.Components[name]
		if !ok {
			continue
		}

		ns := component.EffectiveNamespace(man.Package.Name)
		if ns == "" {
			flat = true
		}

		add(ns)
	}

	return namespaces, flat
}

// hasNativeArtifacts reports whether the staged tree carries any compiled
// module, which pins an otherwise universal archive to the host platform.
func hasNativeArtifacts(stageDir string) (bool, error) {
	found := false

	err := filepath.WalkDir(stageDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && filepath.Ext(path) == ".so" {
			found = true
		}

		return nil
	})
	if err != nil {
		return false, fmt.Errorf("scanning for native artifacts: %w", err)
	}

	return found, nil
}
