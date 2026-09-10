package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tarantool/go-luarocks/client"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/resolve"
	"github.com/tarantool/tt/cli/manifest/rocks"
	"github.com/tarantool/tt/cli/manifest/state"
)

// EnsureDevRocks materializes dev requirements the manifest does not declare
// into the project's .rocks/ tree, so a command can rely on a rock the project
// itself never asked for.
//
// It is the implicit half of the dev closure. tt test drives it: a project that
// has written no [dev_dependencies] still has to be able to run its tests, so
// the runner is resolved as if it had been declared - and only as if. Neither
// app.manifest.toml nor app.manifest.lock is written, which is the whole point:
// a rewrite of either changes the manifest hash, and running the tests would
// then leave the lock stale and the next --locked build refusing to build.
//
// The extra requirements are resolved on top of what the manifest does declare
// and pinned to what the lock already chose, because everything lands in one
// .rocks/ tree and that tree holds one version of a rock. Anything already
// installed at the resolved version is skipped, so a second run of the same
// command fetches nothing.
//
// The build must have run first: the lock is read, never written, so a project
// that has never resolved is an error here rather than a silent resolution.
func EnsureDevRocks(
	ctx context.Context, opts Options, extra map[string]manifest.Dependency,
) error {
	if len(extra) == 0 {
		return nil
	}

	man, err := readManifest(opts.ProjectDir, opts)
	if err != nil {
		return err
	}

	lock, err := loadLock(opts.ProjectDir)
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

	engine := resolve.NewEngine(adapter, opts.ProjectDir, opts.TtVersion)

	closure, warnings, err := engine.ResolveDevExtra(ctx, man, lock, extra)
	if err != nil {
		return fmt.Errorf("resolving dependencies: %w", err)
	}

	opts.emit(warnings)

	missing, err := notInstalled(opts.ProjectDir, closure)
	if err != nil {
		return err
	}

	if len(missing) == 0 {
		return nil
	}

	registryClient, err := adapter.Client(client.BackendLua)
	if err != nil {
		return fmt.Errorf("rocks registry client: %w", err)
	}

	pathClient, err := adapter.Client(client.BackendNative)
	if err != nil {
		return fmt.Errorf("rocks path client: %w", err)
	}

	return materialize(ctx, registryClient, pathClient, opts.ProjectDir,
		manifest.LockProduct{}, missing, adapter.Config().Servers)
}

// notInstalled narrows a resolved closure to the rocks the project tree does
// not already hold at the resolved version.
//
// Installing a rock that is already there is a download and a redeploy with no
// effect, and this closure is resolved on every run of a command that adds it -
// unlike the lock's own, which a build materializes once per lock change. A
// path dependency is always rebuilt: its version says nothing about its
// contents, which is why the lock pins it by content hash instead.
func notInstalled(
	projectDir string, closure []manifest.LockDependency,
) ([]manifest.LockDependency, error) {
	layout, err := state.ResolveLayout(state.ScopeProject, projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving the project tree: %w", err)
	}

	missing := make([]manifest.LockDependency, 0, len(closure))

	for _, dependency := range closure {
		if dependency.Source == sourceRegistry {
			installed, found := state.InstalledVersion(layout, dependency.Name)
			if found && state.SameVersion(installed, dependency.Version) {
				continue
			}
		}

		missing = append(missing, dependency)
	}

	return missing, nil
}
