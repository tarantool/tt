// Package install implements tt package install: it unpacks a .tt package
// archive into a chosen scope and brings the tree to a runnable state. A
// with-deps archive installed into the project scope is a plain offline
// extraction; every other case extracts the package's own files and refetches
// its dependency closure from the registry using the lock's pins.
//
// The one genuinely new problem it solves is joint resolution: several packages
// installed side by side in one project share a single .rocks/ tree, so a
// dependency two of them lock at different versions must be reconciled to one
// version both can accept — or fail with an explanation rather than a silent
// pick. See reconcile.go.
package install

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/rocks"
)

// Source constants mirror the closed set the resolver writes into the lock.
const sourceRegistry = "registry"
const sourcePath = "path"

// Options configures one install run over one or more archives.
type Options struct {
	// ProjectDir is the install-root for project scope (the caller's cwd). It
	// must be absolute; user and system scopes derive their roots from the
	// environment and ignore it.
	ProjectDir string
	// Scope selects where the packages are installed. The zero value ("") is
	// project.
	Scope Scope
	// Archives are the .tt files to install, in order.
	Archives []string
	// Locked refuses an archive whose lock no longer matches its manifest.
	Locked bool
	// Upgrade installs over an already-installed package only when the archive's
	// version is higher; a not-higher --upgrade is a no-op.
	Upgrade bool
	// Force reinstalls over an already-installed package (destructive).
	Force bool
	// Yes skips the confirmation prompt raised when reconciliation changes an
	// installed dependency's version.
	Yes bool
	// Tarantool carries the facts the rocks adapter needs to refetch
	// dependencies; required only when an install refetches from the registry
	// (a --without-deps archive or a reconciled version the archive lacks).
	Tarantool rocks.TarantoolInfo
	// Servers overrides the rock-server list; nil uses the adapter default.
	Servers []string
	// Logger receives the adapter's structured logs; nil disables it.
	Logger *slog.Logger
	// Warn receives non-fatal diagnostics; nil drops them.
	Warn func(string)
	// Confirm is asked before a reconciliation changes an installed dependency's
	// version; returning false aborts that archive. Nil proceeds (the CLI wires a
	// real prompt; Yes bypasses it entirely).
	Confirm func(prompt string) bool
}

func (o Options) warn(msg string) {
	if o.Warn != nil {
		o.Warn(msg)
	}
}

// OneResult reports the outcome of installing a single archive.
type OneResult struct {
	// Archive is the archive path.
	Archive string
	// Package is the installed package's name.
	Package string
	// Version is its version.
	Version string
	// Scope is where it was installed.
	Scope Scope
	// Skipped is set when a --upgrade found nothing newer and did nothing.
	Skipped bool
}

// Failure pairs an archive with the error installing it produced.
type Failure struct {
	Archive string
	Err     error
}

// Result reports what an install run produced across all its archives.
type Result struct {
	// Installed holds one entry per archive that succeeded (Skipped ones
	// included).
	Installed []OneResult
	// Failed holds one entry per archive that failed.
	Failed []Failure
}

// Run installs every archive in opts.Archives into the selected scope and maps
// the outcome to a process exit code through an *ExitError: a single archive
// surfaces its own failure code; a multi-archive run where some succeeded and
// some failed exits 3. A nil error means every archive installed (or was a
// no-op upgrade).
func Run(ctx context.Context, opts Options) (*Result, error) {
	scope, err := ParseScope(string(opts.Scope))
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	opts.Scope = scope

	result := &Result{Installed: nil, Failed: nil}

	for _, archivePath := range opts.Archives {
		one, err := installOne(ctx, opts, archivePath)
		if err != nil {
			result.Failed = append(result.Failed, Failure{Archive: archivePath, Err: err})

			continue
		}

		result.Installed = append(result.Installed, *one)
	}

	return result, aggregateError(result)
}

// aggregateError collapses per-archive outcomes into the run's error: none for
// an all-clear run, a partial-failure (exit 3) when some archives passed and
// some failed, and the single failure's own error otherwise (so a lone archive
// keeps its exit code).
func aggregateError(result *Result) error {
	if len(result.Failed) == 0 {
		return nil
	}

	if len(result.Installed) > 0 {
		total := len(result.Installed) + len(result.Failed)

		return &ExitError{
			Code: exitPartialError,
			Err:  fmt.Errorf("%w (%d of %d)", errPartialInstall, len(result.Failed), total),
		}
	}

	return result.Failed[0].Err
}

// installOne installs a single archive, returning its outcome or a typed error
// carrying the right exit code.
func installOne(ctx context.Context, opts Options, archivePath string) (*OneResult, error) {
	archive, err := OpenArchive(archivePath)
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	header, err := archive.ReadHeader()
	if err != nil {
		return nil, stateErrorf("%w", err)
	}

	// A with-deps archive into user/system is refused from the header alone,
	// before anything touches disk.
	if header.WithDeps && !opts.Scope.AcceptsWithDeps() {
		return nil, stateErrorf("%w (scope %s)", errWithDepsScope, opts.Scope)
	}

	if opts.Locked {
		if want := manifest.HashBytes(header.ManifestBytes); header.Lock.ManifestHash != want {
			return nil, stateErrorf("%w: archive lock does not match its manifest", errLockStale)
		}
	}

	lay, err := resolveLayout(opts.Scope, opts.ProjectDir)
	if err != nil {
		return nil, err
	}

	installed, err := installedPackages(lay, opts.Scope)
	if err != nil {
		return nil, err
	}

	skip, err := checkCollision(opts, header, installed)
	if err != nil {
		return nil, err
	}

	if skip {
		return &OneResult{
			Archive: archivePath, Package: header.Manifest.Package.Name,
			Version: header.Version, Scope: opts.Scope, Skipped: true,
		}, nil
	}

	err = checkRuntime(opts, header, installed)
	if err != nil {
		return nil, err
	}

	err = apply(ctx, opts, archive, header, lay, installed)
	if err != nil {
		return nil, err
	}

	return &OneResult{
		Archive: archivePath, Package: header.Manifest.Package.Name,
		Version: header.Version, Scope: opts.Scope, Skipped: false,
	}, nil
}

// checkCollision enforces the name-collision policy and reports whether the
// install is a no-op (a --upgrade with nothing newer). A collision without
// --force or --upgrade is exit 1.
func checkCollision(opts Options, header *Header, installed []installedPackage) (bool, error) {
	name := header.Manifest.Package.Name

	existing, ok := findInstalled(installed, name)
	if !ok {
		return false, nil
	}

	if existing.primary {
		return false, stateErrorf(
			"%w: %q is the project's primary package, not a guest", errNameCollision, name)
	}

	switch {
	case opts.Force:
		return false, nil
	case opts.Upgrade:
		if !versionHigher(header.Version, existing.version) {
			opts.warn(fmt.Sprintf(
				"%s %s is not newer than the installed %s; nothing to do",
				name, header.Version, existing.version))

			return true, nil
		}

		return false, nil
	default:
		return false, stateErrorf("%w: %q %s (use --upgrade or --force)",
			errNameCollision, name, existing.version)
	}
}

// apply resolves the dependency plan and realizes it: remove stale versions,
// extract the archive, and refetch any registry-sourced dependency.
func apply(
	ctx context.Context, opts Options, archive *Archive,
	header *Header, lay layout, installed []installedPackage,
) error {
	productName, err := selectProduct(header.Manifest)
	if err != nil {
		return err
	}

	plan, err := planDeps(header, productName, installed, lay, header.WithDeps)
	if err != nil {
		return err
	}

	err = confirmReconciliation(opts, plan)
	if err != nil {
		return err
	}

	err = removeStaleVersions(lay, plan)
	if err != nil {
		return err
	}

	extractRuntime := header.WithDeps && !runtimeExists(lay)

	mapper := extractMapper(opts.Scope, plan.skipNames, extractRuntime)

	err = archive.Extract(lay.root, mapper)
	if err != nil {
		return fmt.Errorf("extracting %s: %w", archive.Path(), err)
	}

	err = refetch(ctx, opts, lay, plan)
	if err != nil {
		return err
	}

	return writeMetadata(lay, header)
}

// confirmReconciliation asks for confirmation when the plan changes an
// already-installed dependency's version. --yes and a nil Confirm proceed.
func confirmReconciliation(opts Options, plan installPlan) error {
	var changed []string

	for _, d := range plan.decisions {
		if d.removeVersion != "" {
			changed = append(changed, fmt.Sprintf("%s -> %s", d.name, d.version))
		}
	}

	if len(changed) == 0 || opts.Yes || opts.Confirm == nil {
		return nil
	}

	prompt := fmt.Sprintf("reconciling shared dependencies changes: %v; proceed?", changed)
	if !opts.Confirm(prompt) {
		return stateErrorf("aborted at reconciliation")
	}

	return nil
}

// rockInstaller is the slice of go-luarocks' *client.Rocks the refetch loop
// drives: install a pinned rock into the tree. *client.Rocks satisfies it;
// tests fake it so the loop is exercised without a registry.
type rockInstaller interface {
	Install(ctx context.Context, name string, opts client.InstallOpts) error
}

// refetch installs every registry-sourced dependency of the plan into the tree
// at its reconciled version. It builds the rocks adapter lazily, so an offline
// with-deps install (no registry decisions) never needs Tarantool.
func refetch(ctx context.Context, opts Options, lay layout, plan installPlan) error {
	pending := registryDecisions(plan)
	if len(pending) == 0 {
		return nil
	}

	adapter := rocks.New(rocks.BuildConfig(opts.Tarantool, rocks.ConfigOptions{
		Tree:       lay.tree,
		WorkingDir: lay.root,
		Servers:    opts.Servers,
		Logger:     opts.Logger,
	}))

	rockClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return fmt.Errorf("rocks client: %w", err)
	}

	return installDeps(ctx, rockClient, pending, opts.Servers, opts.warn)
}

// registryDecisions selects the plan's decisions that must be refetched from the
// registry.
func registryDecisions(plan installPlan) []depDecision {
	var pending []depDecision

	for _, d := range plan.decisions {
		if d.source == fromRegistry {
			pending = append(pending, d)
		}
	}

	return pending
}

// installDeps refetches each decision's pinned rock into the tree with
// dependency resolution off: the closure is already complete and ordered, so no
// rock re-resolves its own dependencies — the same discipline tt package fetch
// uses, here against the archive's lock.
func installDeps(
	ctx context.Context, installer rockInstaller,
	decisions []depDecision, servers []string, warn func(string),
) error {
	for _, decision := range decisions {
		if warn != nil {
			warn(fmt.Sprintf("fetching %s %s", decision.name, decision.version))
		}

		installErr := installer.Install(ctx, decision.name, client.InstallOpts{
			Version: decision.version, Servers: servers, Deps: client.DepsNone,
		})
		if installErr != nil {
			return fmt.Errorf("installing %s %s: %w",
				decision.name, decision.version, installErr)
		}
	}

	return nil
}

// runtimeExists reports whether the install-root already holds a _runtime/ tree.
func runtimeExists(lay layout) bool {
	info, err := os.Stat(runtimePath(lay))

	return err == nil && info.IsDir()
}
