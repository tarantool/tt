package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/deps"
	"github.com/tarantool/tt/v3/cli/manifest/projectlock"
	"github.com/tarantool/tt/v3/cli/manifest/resolve"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
	binaryinstall "github.com/tarantool/tt/v3/internal/binary/install"
)

// lockFileName is the lock beside the manifest of a project.
const lockFileName = "app.manifest.lock"

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

// LockProject takes the lock of the project tt works on (projectRootDir), as
// the commands that change a project take it (projectlock.Acquire): the
// context that carries it, which the other functions here are to be given
// so that they do not wait for it again, and its release. It is
// sdk.Project.Lock of the core.
func LockProject(ctx context.Context) (context.Context, func(), error) {
	projectDir, err := projectRootDir()
	if err != nil {
		return ctx, func() {}, err
	}

	return projectlock.Acquire(ctx, projectDir)
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
// on (projectRootDir, as for AddDependency). It is sdk.Project.AddComponent
// of the core.
//
// The tables of the component are appended to the manifest
// (manifest.AppendComponent), every other line and comment kept, and the
// manifest that makes is parsed and checked with the backends of the
// process before it is written in its place (editManifest). A name the
// manifest declares already is ErrComponentDeclared, and nothing is
// written. The lock is not resolved, so no Tarantool is needed: the caller
// resolves it (ResolveProject) when it is to be fresh.
func AddComponent(ctx context.Context, component sdk.Component) error {
	return editManifest(ctx, "with component "+component.Name,
		func(data []byte, current *manifest.Manifest) ([]byte, error) {
			if _, ok := current.Components[component.Name]; ok {
				return nil, fmt.Errorf("%w: %s", ErrComponentDeclared, component.Name)
			}

			block := manifest.ComponentBlock{
				Name:    component.Name,
				Comment: component.Comment,
				Path:    component.Path,
				Include: component.Include,
				Build:   nil,
			}

			if component.Build != nil {
				block.Build = append([]manifest.BlockField{
					{Key: "backend", Value: component.Build.Backend},
				}, blockFields(component.Build.Fields)...)
			}

			return manifest.AppendComponent(data, block)
		})
}

// UpdateComponent changes the component of the manifest of the project tt
// works on as update says (manifest.EditComponent), every other line and
// comment kept, and writes the manifest as AddComponent does, resolving no
// lock. It is sdk.Project.UpdateComponent of the core. A component the
// manifest does not declare is manifest.ErrNoComponent.
func UpdateComponent(ctx context.Context, update sdk.ComponentUpdate) error {
	return editManifest(ctx, "with component "+update.Name+" changed",
		func(data []byte, _ *manifest.Manifest) ([]byte, error) {
			return manifest.EditComponent(data, manifest.ComponentEdit{
				Name:   update.Name,
				Fields: blockFields(update.Fields),
				Build:  blockFields(update.Build),
				Remove: update.Remove,
			})
		})
}

// RemoveComponent removes the component name from the manifest of the
// project tt works on (manifest.RemoveComponent) and writes the manifest as
// AddComponent does, resolving no lock. It is sdk.Project.RemoveComponent
// of the core. A component the manifest does not declare is
// manifest.ErrNoComponent.
func RemoveComponent(ctx context.Context, name string) error {
	return editManifest(ctx, "without component "+name,
		func(data []byte, _ *manifest.Manifest) ([]byte, error) {
			return manifest.RemoveComponent(data, name)
		})
}

// blockFields returns fields as the manifest editor takes them.
func blockFields(fields []sdk.BuildField) []manifest.BlockField {
	block := make([]manifest.BlockField, 0, len(fields))
	for _, field := range fields {
		block = append(block, manifest.BlockField{Key: field.Key, Value: field.Value})
	}

	return block
}

// editManifest edits the manifest of the project tt works on with edit,
// given its source and what it declares, and checks what that makes as
// tt package build checks a manifest, the backends of the process
// included, before it writes it in its place, atomically, with the mode of
// the one it replaces. what says what the manifest is with the edit, for
// the error of a check. A project without a manifest is an error wrapping
// fs.ErrNotExist. The edit is made under the lock of the project
// (projectlock), unless ctx carries it.
func editManifest(
	ctx context.Context, what string, edit func([]byte, *manifest.Manifest) ([]byte, error),
) error {
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	_, release, err := projectlock.Acquire(ctx, projectDir)
	if err != nil {
		return err
	}

	defer release()

	path := filepath.Join(projectDir, manifestFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	current, _, err := manifest.ParseManifest(data)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	edited, err := edit(data, current)
	if err != nil {
		return err
	}

	man, _, err := manifest.ParseManifest(edited)
	if err != nil {
		return fmt.Errorf("the manifest %s does not parse: %w", what, err)
	}

	_, err = man.Validate(buildreg.Process())
	if err != nil {
		return fmt.Errorf("the manifest %s is invalid: %w", what, err)
	}

	return writeFileAtomic(path, edited)
}

// RemoveDependency removes the dependency name from the manifest of the
// project tt works on and re-resolves the lock without it, holding every
// other version: deps.Remove, what tt package remove does. It is
// sdk.Project.RemoveDependency of the core; registries are the --registry
// layer of the rock servers. A tt with no Tarantool fails with
// ErrNoTarantool, as AddDependency does.
func RemoveDependency(ctx context.Context, name string, registries []string) (
	*deps.Result, error,
) {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return nil, err
	}

	//nolint:contextcheck // tarantool --version is asked once, and takes no context.
	opts, err := dependencyOptionsIn(projectDir, registries)
	if err != nil {
		return nil, err
	}

	return deps.Remove(ctx, opts, name)
}

// ResolveProject re-resolves the lock of the project tt works on against
// its manifest, holding every version it chose: deps.Resolve, what tt
// package resolve does. It is sdk.Project.Resolve of the core; registries
// are the --registry layer of the rock servers. A tt with no Tarantool
// resolves without the facts of one, as tt package build resolves a stale
// lock (buildTarantoolInfo).
func ResolveProject(ctx context.Context, registries []string) (*deps.Result, error) {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return nil, err
	}

	//nolint:contextcheck // tarantool --version is asked once, and takes no context.
	tntInfo, err := buildTarantoolInfo()
	if err != nil {
		return nil, err
	}

	opts, err := dependencyOptionsWith(projectDir, registries, tntInfo)
	if err != nil {
		return nil, err
	}

	return deps.Resolve(ctx, opts)
}

// ProjectLock is the lock of the project tt works on, as ReadProjectLock
// reads it.
type ProjectLock struct {
	// Manifest is the manifest of the project, parsed and not checked.
	Manifest *manifest.Manifest
	// Lock is its lock.
	Lock *manifest.Lock
	// Stale reports a lock that is not that of the manifest, Reason why.
	Stale  bool
	Reason string
}

// ReadProjectLock reads the manifest and the lock of the project tt works
// on, and whether the lock is fresh (resolve.IsStale), resolving nothing.
// It is sdk.Project.ReadLock of the core. A missing manifest or lock is an
// error wrapping fs.ErrNotExist.
func ReadProjectLock() (ProjectLock, error) {
	projectDir, err := projectRootDir()
	if err != nil {
		return ProjectLock{}, err
	}

	path := filepath.Join(projectDir, manifestFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectLock{}, fmt.Errorf("reading %s: %w", path, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return ProjectLock{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	lockPath := filepath.Join(projectDir, lockFileName)

	data, err = os.ReadFile(lockPath)
	if err != nil {
		return ProjectLock{}, fmt.Errorf("reading %s: %w", lockPath, err)
	}

	lock, err := manifest.ParseLock(data)
	if err != nil {
		return ProjectLock{}, fmt.Errorf("parsing %s: %w", lockPath, err)
	}

	stale, reason, err := resolve.IsStale(projectDir, man, lock)
	if err != nil {
		return ProjectLock{}, fmt.Errorf("checking %s: %w", lockPath, err)
	}

	return ProjectLock{Manifest: man, Lock: lock, Stale: stale, Reason: reason}, nil
}

// BuildProject builds the project tt works on as tt package build does
// (buildPackageIn), what opts says. It is sdk.Project.Build of the core.
// opts.ShowOutput shows what the tools print as tt -V does.
func BuildProject(ctx context.Context, opts sdk.BuildOptions) (*build.Result, error) {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return nil, err
	}

	return buildPackageResult(ctx, projectDir, packageRun{
		product:    opts.Product,
		component:  "",
		components: opts.Components,
		locked:     opts.Locked,
		fetchOnly:  false,
		rebuild:    opts.Rebuild,
		jobs:       opts.Jobs,
		registries: opts.Registries,
		showOutput: opts.ShowOutput,
		args:       opts.Args,
	})
}

// CleanProject removes what the builds of the project tt works on made, as
// tt package clean does (build.Clean), what opts says. It is
// sdk.Project.Clean of the core. A component the manifest does not declare
// is an error wrapping manifest.ErrNoComponent.
func CleanProject(ctx context.Context, opts sdk.CleanOptions) (*build.CleanResult, error) {
	ctx, stop := packageContextOf(ctx)
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return nil, err
	}

	return cleanProjectIn(ctx, projectDir, build.CleanOptions{
		Components: opts.Components, DryRun: opts.DryRun,
	})
}

// ComponentContext returns the Context of the build of the component opts
// names of the project tt works on, and the build of its block, building
// nothing (build.ComponentContext). It is sdk.Project.BuildContext of the
// core.
func ComponentContext(opts sdk.BuildContextOptions) (*sdkbuild.Context, sdkbuild.Build, error) {
	projectDir, err := projectRootDir()
	if err != nil {
		return nil, nil, err
	}

	tntInfo, err := buildTarantoolInfo()
	if err != nil {
		return nil, nil, err
	}

	return build.ComponentContext(build.Options{
		ProjectDir: projectDir,
		Product:    opts.Product,
		Component:  "",
		Components: nil,
		Args:       opts.Args,
		Locked:     opts.Locked,
		FetchOnly:  false,
		TtVersion:  lockStamp(),
		Tarantool:  tntInfo,
		Registries: manifestrocks.Sources{
			Flag: nil, Env: nil, Manifest: nil, WorkingDir: "", ProjectDir: "",
		},
		ShowOutput: cmdCtx.Cli.Verbose,
		Rebuild:    false,
		Jobs:       opts.Jobs,
		Now:        time.Time{},
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
		Removed:    nil,
		Check:      nil,
		Backends:   buildreg.Process(),
	}, opts.Component)
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
		components: nil,
		locked:     false,
		fetchOnly:  true,
		rebuild:    false,
		jobs:       0,
		registries: registries,
		showOutput: false,
		args:       nil,
	})
}
