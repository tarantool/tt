package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk"
	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/native"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// This file runs the build backends of the components of a project and of
// its hooks: what the registry of the build (Options.Backends) has, the
// core's and the modules' alike, each in a sdkbuild.Context the build makes
// for it (builder).

// builder makes the sdkbuild.Context of the build of each component of a
// project into its stage, and of the hooks of a package.
type builder struct {
	opts  Options
	proj  project
	stage string
	// target is the platform the build is for: the machine tt runs on.
	target sdkbuild.Target
	// tarantool is the Tarantool the build is for.
	tarantool sdkbuild.Tarantool
	// toolchain is how tt compiles a C module.
	toolchain sdkbuild.Toolchain
	// version is TT_VERSION, empty before the version is derived: when the
	// builds are only preflighted.
	version string
}

// newBuilder returns the builder of the builds of proj into stage, the
// toolchain that of adapter, at the version ver.
func newBuilder(
	opts Options, proj project, adapter *rocks.Adapter, stage string, ver version.Version,
) builder {
	host := native.Host()
	prefix, includeDir := tarantoolDirs(opts.Tarantool)

	return builder{
		opts:   opts,
		proj:   proj,
		stage:  stage,
		target: sdkbuild.Target{OS: host.OS, Arch: host.Arch},
		tarantool: sdkbuild.Tarantool{
			Prefix: prefix, IncludeDir: includeDir, Version: opts.Tarantool.Version,
		},
		toolchain: toolchain(adapter.Flags()),
		version:   ver.SemVer,
	}
}

// componentRun is a component of a build with its backend and the build
// the backend made of its block, ready to run in its Context.
type componentRun struct {
	name      string
	component manifest.Component
	backend   sdkbuild.Backend
	build     sdkbuild.Build
	context   *sdkbuild.Context
	// outputs are the files of the output of its block, each with its type.
	outputs []sdkbuild.OutputFile
}

// component returns the run of the component name, which has a build block:
// its backend checks the block again (buildreg.Registry.Build), as Validate
// did, and the run gets its Context. A block of a backend the registry does
// not have is buildreg.ErrNoBackend.
//
// TT_OUTPUT_DIR is the directory of the modules, but for a component of
// kind = "executable" (manifest_version 0.1, deprecated), whose build was
// told to put its programs there: it is the directory of the programs then.
func (b builder) component(name string) (componentRun, error) {
	component := b.proj.man.Components[name]
	target := b.proj.man.ComponentTarget(name)

	backend, bld, err := b.opts.backends().Build("components."+name+".build", target,
		*component.Build)
	if err != nil {
		return componentRun{}, err
	}

	dirs := sdkbuild.Dirs{
		Project:   b.proj.dir,
		Component: resolveDir(b.proj.dir, component.Path),
		Cwd:       backendCwd(b.proj.dir, component),
		Work:      componentBuildDir(b.proj.stateDir, name),
		Output:    componentOutputDir(b.stage, target.Namespace),
		Share:     componentShareDir(b.stage, target.Namespace),
		Programs:  filepath.Join(b.stage, rocktree.StageBin),
		State:     b.proj.stateDir,
		Tools:     ProjectBinDir(b.proj.dir),
		Tree:      filepath.Join(b.opts.ProjectDir, rocksDirName),
	}

	outputDir := dirs.Output
	if component.ProgramsOnly() {
		outputDir = dirs.Programs
	}

	outputs := buildreg.OutputFiles(backend, component.Build.Output, component.Kind)

	env := environment{
		block:       component.Build.Env,
		projectRoot: b.proj.dir,
		contract: contract{
			outputDir:   outputDir,
			component:   name,
			buildDir:    dirs.Work,
			projectRoot: b.proj.dir,
			pkg:         b.proj.man.Package.Name,
			version:     b.version,
			target:      b.target,
			tarantool:   b.tarantool,
			hook:        false,
		}.environ(),
	}

	facts := sdkbuild.Context{
		Component: sdkbuild.Component{
			Name: name, Path: target.Path, Namespace: target.Namespace,
			Package: target.Package, Expects: buildreg.Expects(target.Kind),
		},
		Target:    b.target,
		Dirs:      dirs,
		Tarantool: b.tarantool,
		Toolchain: b.toolchain,
		Env:       env.variables(),
		Fresh:     b.opts.Rebuild,
		Locked:    b.opts.Locked,
		Static:    component.Build.StaticValue(),
		Hook:      false,
		Log:       buildLogger(backend.Name),
		Warn:      b.warn,
	}

	return componentRun{
		name: name, component: component, backend: backend, build: bld,
		context: sdkbuild.NewContext(facts, b.host(env, dirs.Cwd)), outputs: outputs,
	}, nil
}

// hookContext returns the Context of the given hook of the package, run by
// the backend named backend: the reduced contract of a hook, with no
// component, no work and output directories and none of their variables.
func (b builder) hookContext(hook manifest.Hook, backend string) *sdkbuild.Context {
	dirs := sdkbuild.Dirs{
		Project:   b.proj.dir,
		Component: "",
		Cwd:       resolveDir(b.proj.dir, hook.Cwd),
		Work:      "",
		Output:    "",
		Share:     "",
		Programs:  "",
		State:     b.proj.stateDir,
		Tools:     ProjectBinDir(b.proj.dir),
		Tree:      filepath.Join(b.opts.ProjectDir, rocksDirName),
	}

	env := environment{
		block:       hook.Env,
		projectRoot: b.proj.dir,
		contract: contract{
			outputDir:   "",
			component:   "",
			buildDir:    "",
			projectRoot: b.proj.dir,
			pkg:         b.proj.man.Package.Name,
			version:     b.version,
			target:      b.target,
			tarantool:   b.tarantool,
			hook:        true,
		}.environ(),
	}

	facts := sdkbuild.Context{
		Component: sdkbuild.Component{Name: "", Path: "", Namespace: "", Package: "", Expects: nil},
		Target:    b.target,
		Dirs:      dirs,
		Tarantool: b.tarantool,
		Toolchain: b.toolchain,
		Env:       env.variables(),
		Fresh:     b.opts.Rebuild,
		Locked:    b.opts.Locked,
		Static:    false,
		Hook:      true,
		Log:       buildLogger(backend),
		Warn:      b.warn,
	}

	return sdkbuild.NewContext(facts, b.host(env, dirs.Cwd))
}

// host returns the host of a build whose commands run in cwd with env.
func (b builder) host(env environment, cwd string) host {
	return host{
		env: env, cwd: cwd, showOutput: b.opts.ShowOutput, pinned: pinnedVersions(b.proj.pinned),
	}
}

// warn shows a warning of a build.
func (b builder) warn(warning string) {
	b.opts.emit([]string{warning})
}

// preflight checks, for each of the selected components of the builder's
// project, that the registry of the build has its backend, which Validate
// let through with a warning otherwise, then what its build needs before any
// of them runs (sdkbuild.Preflighter): a missing backend or tool then fails
// the build before it has resolved the closure and compiled the components
// ahead of it.
//
// A tool is looked for where the build looks for it: in the PATH of the
// component's [build].env, after .tarantool/bin of the project being built -
// the package, or a path dependency in its own directory.
func (b builder) preflight(ctx context.Context, components []string) error {
	for _, name := range components {
		if b.proj.man.Components[name].Build == nil {
			continue
		}

		run, err := b.component(name)
		if err != nil {
			return sdk.Errorf(sdk.ExitFailure, "building component %q: %w", name, err)
		}

		preflighter, ok := run.build.(sdkbuild.Preflighter)
		if !ok {
			continue
		}

		err = preflighter.Preflight(ctx, run.context)
		if err != nil {
			return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
		}
	}

	return nil
}

// runBackends runs the build backend of every selected component that declares
// one, in the product's component order, each file it makes going to the
// directory of the package's stage of its type: the component's lib
// namespace directory, its share directory, the bin directory. A backend
// failure is exit code 2.
//
// It returns the record of what each component built, by type, those of the
// components not built this time as an earlier build recorded them, and,
// for each component it built or skipped, the absolute paths of the files of
// the stage the backend left there (runComponent). What an earlier build of
// a component made and is still there counts as made: what the record says,
// and what last, the record of the stage, says it wrote there.
func (b builder) runBackends(
	ctx context.Context, components []string, last stageRecord,
) (map[string]madeFiles, map[string][]string, error) {
	results := readResults(b.proj.stateDir, b.opts.Warn)
	written := map[string][]string{}
	built := false

	for _, name := range components {
		if b.proj.man.Components[name].Build == nil {
			continue
		}

		run, err := b.component(name)
		if err != nil {
			return nil, nil, fmt.Errorf("component %q: %w", name, err)
		}

		dirs := run.context.Dirs

		err = prepareBuildDir(dirs.Work, b.opts.Rebuild)
		if err != nil {
			return nil, nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", name, err)
		}

		recorded := results[name].join(madeIn(dirs, joinDir(b.stage,
			last.Components[name].Written)))

		made, err := b.runComponent(ctx, run, recorded)
		if err != nil {
			return nil, nil, err
		}

		results[name] = made
		written[name] = made.paths(dirs)
		built = true
	}

	if !built {
		return keepDeclared(b.proj.man, results), written, nil
	}

	results, err := writeResults(b.proj.stateDir, b.proj.man, results)
	if err != nil {
		return nil, nil, err
	}

	return results, written, nil
}

// runComponent runs the build of a component and returns what it made, by
// type (runWritingBackend); recorded is what an earlier build of it made,
// of which only the types its backend produces are taken. What it made is
// checked (checkMade). An incremental one is skipped when its
// inputs are those of its last build and what that build left is still there
// (see skipUnchanged), and then made what that build did; its build records
// what it made and the files of the component the layout takes. Those, and
// the modules it made, are the files of a static one the build checks
// (checkStaticModule), before the record is written: a module that fails the
// check is built again.
func (b builder) runComponent(
	ctx context.Context, run componentRun, recorded madeFiles,
) (madeFiles, error) {
	incremental := run.component.Build.IncrementalValue()
	static := run.component.Build.StaticValue()

	var state incrementalState

	if incremental {
		var (
			skipped bool
			err     error
		)

		state, skipped, err = b.skipUnchanged(ctx, run)
		if err != nil {
			return madeFiles{}, err
		}

		if skipped {
			return state.last.made(), nil
		}
	}

	// What an earlier build made of a type the backend makes none of - one
	// of another backend, say - is not taken as made: the stage drops it.
	made, err := runWritingBackend(ctx, run,
		recorded.join(state.last.made()).only(run.backend.Produces))
	if err == nil {
		err = checkMade(run, b.stage, made)
	}

	if err != nil || !incremental && !static {
		return made, err
	}

	compAbsPath := run.context.Dirs.Component

	var laidOut []string

	if laysOut(run.component, run.backend) {
		laidOut, err = laidOutFiles(run.component, compAbsPath, run.backend.Exclude)
		if err != nil {
			return madeFiles{}, sdk.Errorf(sdk.ExitSystem, "building component %q: %w",
				run.name, err)
		}
	}

	if static {
		err = checkStaticModule(b.opts, run.name, moduleFiles(run.context.Dirs.Output,
			made.Modules, compAbsPath, laidOut))
		if err != nil {
			return madeFiles{}, err
		}
	}

	if incremental {
		err = writeIncremental(b.proj.stateDir, run.name, incrementalRecord{
			Inputs:   state.inputs,
			Written:  made.Modules,
			Shared:   made.Data,
			Programs: made.Programs,
			LaidOut:  laidOut,
		})
		if err != nil {
			return madeFiles{}, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name,
				err)
		}
	}

	return made, nil
}

// runWritingBackend runs the backend of a component and returns what it
// made, by type: what it wrote into its output and share directories,
// relative to each (writtenFiles) - the files it created or changed, and
// those of recorded, which an earlier build wrote, that are still there, as
// a make target that is up to date writes nothing - and the programs it put
// into the bin directory (madePrograms).
func runWritingBackend(ctx context.Context, run componentRun, recorded madeFiles) (
	madeFiles, error,
) {
	dirs := run.context.Dirs

	stamp := func() (map[string]fileStamp, map[string]fileStamp, error) {
		output, err := stampTree(dirs.Output)
		if err != nil {
			return nil, nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name, err)
		}

		share, err := stampTree(dirs.Share)
		if err != nil {
			return nil, nil, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name, err)
		}

		return output, share, nil
	}

	outputBefore, shareBefore, err := stamp()
	if err != nil {
		return madeFiles{}, err
	}

	programsBefore, err := stampDir(dirs.Programs)
	if err != nil {
		return madeFiles{}, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name, err)
	}

	err = runComponentBackend(ctx, run)
	if err != nil {
		return madeFiles{}, err
	}

	outputAfter, shareAfter, err := stamp()
	if err != nil {
		return madeFiles{}, err
	}

	programs, err := madePrograms(run, programsBefore, recorded.Programs)
	if err != nil {
		return madeFiles{}, err
	}

	return madeFiles{
		Modules:  writtenFiles(outputBefore, outputAfter, recorded.Modules),
		Data:     writtenFiles(shareBefore, shareAfter, recorded.Data),
		Programs: programs,
	}, nil
}

// runComponentBackend runs the build of a component: it creates the output,
// share and programs directories, runs the build and, for a backend whose
// output the core copies (sdkbuild.OutputCwd), copies the files output lists
// from the working directory into the directories of their types.
func runComponentBackend(ctx context.Context, run componentRun) error {
	dirs := run.context.Dirs

	err := makeDirs(dirs.Output, dirs.Share, dirs.Programs)
	if err == nil {
		err = run.build.Run(ctx, run.context)
	}

	if err == nil && run.backend.OutputFrom == sdkbuild.OutputCwd {
		err = copyOutputs(dirs, dirs.Cwd, run.outputs)
	}

	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name, err)
	}

	return nil
}

// makeDirs creates each of dirs that is not empty.
func makeDirs(dirs ...string) error {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}

		err := os.MkdirAll(dir, dirPerm)
		if err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	return nil
}

// copyOutputs copies each file of outputs from cwd into the directory of
// dirs of its type, flattened to its base name (matching how the loader
// looks up <namespace>/<name>, and tt instance a program in .rocks/bin/). A
// listed file that does not exist after the build is an error: the build
// claimed an output it did not make.
func copyOutputs(dirs sdkbuild.Dirs, cwd string, outputs []sdkbuild.OutputFile) error {
	for _, entry := range outputs {
		err := sdkbuild.CopyFile(filepath.Join(dirs.Of(entry.To), filepath.Base(entry.Path)),
			filepath.Join(cwd, entry.Path))
		if err != nil {
			return fmt.Errorf("copy declared output %q: %w", entry.Path, err)
		}
	}

	return nil
}

// outputPaths returns the paths of the files of outputs.
func outputPaths(outputs []sdkbuild.OutputFile) []string {
	paths := make([]string, 0, len(outputs))
	for _, entry := range outputs {
		paths = append(paths, entry.Path)
	}

	return paths
}

// skipUnchanged hashes the inputs of an incremental component and reports
// whether its build can be skipped: they are those of its last build, and
// what that build left is still there - the files its output lists, the
// files of the component the layout took and the files it made into its
// output, share and programs directories. The files output lists are copied
// into their directories before the last are looked for, as the build itself
// copies them (sdkbuild.OutputCwd): the tree may have been cleared since.
func (b builder) skipUnchanged(ctx context.Context, run componentRun) (
	incrementalState, bool, error,
) {
	var state incrementalState

	dirs := run.context.Dirs

	inputs, err := b.inputs(ctx, run)
	if err != nil {
		return state, false, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name,
			err)
	}

	state.inputs = inputs

	last, found := readIncremental(b.proj.stateDir, run.name)
	if found {
		state.last = last
	}

	var outputs []sdkbuild.OutputFile

	if run.backend.OutputFrom == sdkbuild.OutputCwd {
		outputs = run.outputs
	}

	if b.opts.Rebuild || !found || last.Inputs != inputs ||
		!allPresent(dirs.Cwd, outputPaths(outputs)) || !allPresent(dirs.Component, last.LaidOut) {
		return state, false, nil
	}

	err = copyOutputs(dirs, dirs.Cwd, outputs)
	if err != nil {
		return state, false, sdk.Errorf(sdk.ExitSystem, "building component %q: %w", run.name,
			err)
	}

	if !allPresent(dirs.Output, last.Written) || !allPresent(dirs.Share, last.Shared) ||
		!allPresent(dirs.Programs, last.Programs) {
		return state, false, nil
	}

	// The core's own record, which tt -V shows; Options.Logger is that of
	// go-luarocks, whose records say so.
	log.Debug("component is up to date", "component", run.name)

	return state, true, nil
}

// inputs hashes the inputs of the build of run (componentInputs), those its
// build names besides (sdkbuild.InputHasher) included.
func (b builder) inputs(ctx context.Context, run componentRun) (string, error) {
	var extra []string

	if hasher, ok := run.build.(sdkbuild.InputHasher); ok {
		var err error

		extra, err = hasher.Inputs(ctx, run.context)
		if err != nil {
			return "", fmt.Errorf("the inputs of the build: %w", err)
		}
	}

	return componentInputs(b.proj.dir, run.context.Dirs.Component, *run.component.Build, extra)
}

// errNotHook reports a hook whose backend runs no hook, which Validate
// refuses.
var errNotHook = errors.New("the backend runs no hook")

// runHook runs the named lifecycle hook if the manifest of the package
// declares it, under the reduced hook contract (builder.hookContext). An
// absent hook is a no-op. The hook runs in its own cwd (default: the project
// root), so pre_build can generate files anywhere in the tree before
// components are gathered. A hook is told where Tarantool is as a build is
// (see tarantoolDirs).
//
// pre_build runs before the dependency closure is materialized, so .rocks/
// may not exist yet and PATH is not extended to reach it. That ordering is
// what lets a hook produce the contents of a path dependency, whose hash and
// rockspec the resolver reads immediately afterwards. Generation that feeds
// the artifact belongs in a component's build backend instead, which runs
// after materialization and is handed TT_OUTPUT_DIR.
func (b builder) runHook(ctx context.Context, name string) error {
	hook, ok := b.proj.man.Hooks[name]
	if !ok {
		return nil
	}

	backend, bld, err := b.opts.backends().Build("hooks."+name, b.proj.man.HookTarget(), hook)
	if err == nil && !backend.Hook {
		err = fmt.Errorf("%w: %q", errNotHook, backend.Name)
	}

	if err == nil {
		err = bld.Run(ctx, b.hookContext(hook, backend.Name))
	}

	if err != nil {
		return fmt.Errorf("%s hook: %w", name, err)
	}

	return nil
}

// sourceLayout reports whether the files of component are laid out, and
// returns the patterns of those its backend has the layout leave out
// (sdkbuild.Backend.Exclude), as backends has it (laysOut).
func sourceLayout(backends *buildreg.Registry, component manifest.Component) (bool, []string) {
	var backend sdkbuild.Backend

	if component.Build != nil {
		backend, _, _ = backends.Backend(component.Build.Backend)
	}

	return laysOut(component, backend), backend.Exclude
}

// laysOut reports whether the files of component, built by backend, are laid
// out: not for a component of kind = "executable" (manifest_version 0.1,
// deprecated), which says its build makes everything it has, and not for a
// backend whose sources are no files of the package
// (sdkbuild.Backend.NoSourceLayout) but those the include of the component
// names.
func laysOut(component manifest.Component, backend sdkbuild.Backend) bool {
	switch {
	case component.ProgramsOnly():
		return false
	case backend.NoSourceLayout:
		return len(component.Include) > 0
	default:
		return true
	}
}
