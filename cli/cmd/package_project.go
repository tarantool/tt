package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/deps"
	binaryinstall "github.com/tarantool/tt/v3/internal/binary/install"
)

// anyVersion is the constraint of a dependency declared with none, as
// tt package add declares one: whatever the rock servers serve.
const anyVersion = "*"

// ErrComponentDeclared reports a component AddComponent is to declare that
// the manifest declares already.
var ErrComponentDeclared = errors.New("the manifest declares the component already")

// manifestPerm is the mode of a manifest AddComponent writes where there was
// none to keep.
const manifestPerm os.FileMode = 0o644

// ProjectRootDir returns the project a module's project works on
// (sdk.Project.Root): the project the working directory, or -C, is in, as
// the project commands of tt find it, saying once in the process that it is
// above the working directory when it is (projectRootDir).
func ProjectRootDir() (string, error) {
	return projectRootDir()
}

// AddDependency declares dep in the manifest of the project tt works on
// (projectRootDir, the project the working directory is in, as tt package
// add finds it) and re-resolves the lock, holding every version it already
// chose: deps.Add, what tt package add does, for a dependency without a
// registry, and deps.AddFromRegistry, the long form, for one with a
// registry. It is sdk.Project.AddDependency of the core, which says what
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

	projectDir, err := projectRootDir()
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

// AddComponent declares component in the manifest of the project tt works
// on (projectRootDir, as for AddDependency) and re-resolves the lock against
// it, holding every version it already chose, as tt package resolve does
// (deps.Resolve). It is sdk.Project.AddComponent of the core.
//
// The tables of the component are appended to the manifest
// (manifest.AppendComponent), every other line and comment kept, and the
// manifest that makes is parsed and checked with the backends of the
// process before it is written in its place, atomically, with the mode of
// the one it replaces. declared reports that it was written: a resolution
// that fails leaves the component declared, and its error says that the
// lock was not resolved again. A name the manifest declares already is
// ErrComponentDeclared, and nothing is written.
//
// The warnings of the resolution go to the process logger. It stops on
// SIGINT and SIGTERM, and when ctx is done. A tt with no Tarantool fails
// with ErrNoTarantool before anything is written.
func AddComponent(ctx context.Context, component sdk.Component) (*deps.Result, bool, error) {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return nil, false, err
	}

	//nolint:contextcheck // tarantool --version is asked once, and takes no context.
	opts, err := dependencyOptionsIn(projectDir, component.Registries)
	if err != nil {
		return nil, false, err
	}

	path := filepath.Join(projectDir, manifestFileName)

	err = declareComponent(path, component)
	if err != nil {
		return nil, false, err
	}

	result, err := deps.Resolve(ctx, opts)
	if err != nil {
		return nil, true, fmt.Errorf("re-resolving the lock: %w", err)
	}

	return result, true, nil
}

// declareComponent appends component to the manifest at path (AddComponent).
func declareComponent(path string, component sdk.Component) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	current, _, err := manifest.ParseManifest(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	if _, ok := current.Components[component.Name]; ok {
		return fmt.Errorf("%w: %s", ErrComponentDeclared, component.Name)
	}

	block := manifest.ComponentBlock{
		Name:    component.Name,
		Comment: component.Comment,
		Path:    component.Path,
		Include: component.Include,
		Build:   nil,
	}

	if component.Build != nil {
		block.Build = append(block.Build,
			manifest.BlockField{Key: "backend", Value: component.Build.Backend})

		for _, field := range component.Build.Fields {
			block.Build = append(block.Build,
				manifest.BlockField{Key: field.Key, Value: field.Value})
		}
	}

	edited, err := manifest.AppendComponent(data, block)
	if err != nil {
		return err
	}

	man, _, err := manifest.ParseManifest(edited)
	if err != nil {
		return fmt.Errorf("the manifest with component %s does not parse: %w", component.Name,
			err)
	}

	_, err = man.Validate(buildreg.Process())
	if err != nil {
		return fmt.Errorf("the manifest with component %s is invalid: %w", component.Name, err)
	}

	return writeFileAtomic(path, edited)
}

// writeFileAtomic writes data to path through a temporary file in its
// directory and a rename, with the mode of the file it replaces.
func writeFileAtomic(path string, data []byte) error {
	mode := manifestPerm

	info, err := os.Stat(path)
	if err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}

	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// PrepareProjectEnvironment writes .env/activate.sh and deactivate.sh of
// the project tt works on (projectRootDir) when they are missing, as the
// first tt binary install into the project scope does. It is
// sdk.Project.PrepareEnvironment of the core.
func PrepareProjectEnvironment() error {
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	return binaryinstall.PrepareProjectEnvironmentIn(projectDir)
}

// FetchRocks materializes .rocks/ of the project tt works on
// (projectRootDir, as for AddDependency) strictly from its lock, as tt
// package fetch does for the default product: no resolution, no component
// builds. registries is the --registry layer of the rock servers. It is
// sdk.Project.Install of the core.
//
// The warnings go to the process logger. The fetch stops on SIGINT and
// SIGTERM, killing the build tools a rock runs, as tt package fetch does, and
// when ctx is done. A tt with no Tarantool installs the rocks that need none,
// as tt package fetch does (buildTarantoolInfo).
func FetchRocks(ctx context.Context, registries []string) error {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := projectRootDir()
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
