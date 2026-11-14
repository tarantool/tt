package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/scope"
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

// manifestOf is what man declares in the SDK's terms.
func manifestOf(man *manifest.Manifest) sdk.Manifest {
	view := sdk.Manifest{
		Dependencies:    dependenciesOf(man.Dependencies),
		DevDependencies: dependenciesOf(man.DevDependencies),
		Components:      make(map[string]sdk.ManifestComponent, len(man.Components)),
	}

	for name, component := range man.Components {
		declared := sdk.ManifestComponent{Path: component.Path, Build: nil}

		if component.Build != nil {
			declared.Build = &sdk.ManifestBuild{
				Backend: component.Build.Backend, Cwd: component.Build.Cwd,
			}
		}

		view.Components[name] = declared
	}

	return view
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

// AddComponent declares component in the manifest of Root and re-resolves
// the lock: cmd.AddComponent, its result told in the SDK's terms.
func (project) AddComponent(
	ctx context.Context, component sdk.Component,
) (sdk.ComponentChange, error) {
	result, declared, err := cmd.AddComponent(ctx, component)
	if err != nil {
		return sdk.ComponentChange{Declared: declared, Moves: nil}, packageError(err)
	}

	return sdk.ComponentChange{Declared: declared, Moves: movesOf(result.Moves)}, nil
}

// StateDir returns .tarantool of Root.
func (p project) StateDir() (string, error) {
	root, err := p.Root()
	if err != nil {
		return "", err
	}

	return filepath.Join(root, scope.ProjectDirName), nil
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
