package cmd

import (
	"context"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest/deps"
)

// anyVersion is the constraint of a dependency declared with none, as
// tt package add declares one: whatever the rock servers serve.
const anyVersion = "*"

// AddDependency declares dep in the manifest of the project tt works on
// (ProjectDir, the directory itself: no project is searched for above it, as
// sdk.Project.Dir promises) and re-resolves the lock, holding every version
// it already chose: deps.Add, what tt package add does, for a dependency
// without a registry, and deps.AddFromRegistry, the long form, for one with
// a registry. It is sdk.Project.AddDependency of the core, which says what
// becomes of a declaration already there; dep.Registries is the --registry
// layer of the rock servers.
//
// Nothing is printed: the warnings go to the process logger, the changes are
// in the result. The resolution stops on SIGINT and SIGTERM, as the one of a
// tt package command does, and when ctx is done. A tt with no Tarantool
// fails with ErrNoTarantool.
func AddDependency(ctx context.Context, dep sdk.Dependency) (*deps.Result, error) {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := ProjectDir()
	if err != nil {
		return nil, err
	}

	//nolint:contextcheck // tarantool --version is asked once, and takes no context.
	opts, err := dependencyOptionsIn(projectDir, dep.Registries)
	if err != nil {
		return nil, err
	}

	if dep.Registry == "" {
		return deps.Add(ctx, opts, dep.Name, dep.Constraint, dep.Dev)
	}

	constraint := dep.Constraint
	if constraint == "" {
		constraint = anyVersion
	}

	return deps.AddFromRegistry(ctx, opts, dep.Name, constraint, dep.Registry, dep.Dev)
}

// FetchRocks materializes .rocks/ of the project tt works on (ProjectDir,
// as for AddDependency) strictly from its lock, as tt package fetch does for
// the default product: no resolution, no component builds. registries is the
// --registry layer of the rock servers. It is sdk.Project.Install of the
// core.
//
// The warnings go to the process logger. The fetch stops on SIGINT and
// SIGTERM, killing the build tools a rock runs, as tt package fetch does, and
// when ctx is done. A tt with no Tarantool installs the rocks that need none,
// as tt package fetch does (buildTarantoolInfo).
func FetchRocks(ctx context.Context, registries []string) error {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := ProjectDir()
	if err != nil {
		return err
	}

	return buildPackageIn(ctx, projectDir, packageRun{
		product:    "",
		component:  "",
		locked:     false,
		fetchOnly:  true,
		rebuild:    false,
		registries: registries,
	})
}
