package pack

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/rockscript"
)

// This file asks the builds of a package for their exports
// (sdkbuild.Exporter), of which tt-rock.sh, the script that builds the
// package without tt, is made (rockscript).

// exported is what the builds of a package exported (exportBuilds).
type exported struct {
	// script is the script of the builds, but its layout, which
	// releaseBuild plans.
	script rockscript.Script
	// ttOnly are the builds that do not export, why each, a sentence each:
	// when there is any, the package builds with tt alone, and script is to
	// be dropped.
	ttOnly []string
}

// exportBuilds asks the builds of the product of built - those of its
// components that have a build block, in its order, and the hooks a build
// runs - for their exports, the backends of backends making them of their
// blocks as a build does (buildreg.Registry.Build), and makes the script of
// them. A build exports when its backend says its builds are made without tt
// (sdkbuild.Backend.Exports) and its Export does not fail with
// sdkbuild.ErrNoExport, when its working directory is in the project, whose
// files the sources of the rock are, when the script can set the env of its
// block (rockscript.CheckEnv), and when no value of it the script would hold
// has a NUL byte (rockscript.CheckStep). Any other failure of an Export
// fails the pack.
func exportBuilds(built *build.Result, backends *buildreg.Registry) (exported, error) {
	man := built.Manifest
	locked := lockedVersions(built.Lock, built.Product)

	result := exported{
		script: rockscript.Script{
			Package: man.Package.Name, Version: built.Version.SemVer, PreBuild: nil,
			PostBuild: nil, Components: nil, Lay: nil,
		},
		ttOnly: nil,
	}

	for _, name := range man.EffectiveProducts()[built.Product].Components {
		component := man.Components[name]
		if component.Build == nil {
			continue
		}

		target := man.ComponentTarget(name)

		cwd := component.Build.Cwd
		if cwd == "" {
			cwd = component.Path
		}

		step, reason, err := exportStep(backends, fmt.Sprintf("the component %q", name),
			"components."+name+".build", target, *component.Build, cwd, locked)
		if err != nil {
			return exported{}, err
		}

		if reason != "" {
			result.ttOnly = append(result.ttOnly, reason)

			continue
		}

		step.Component = name
		step.Namespace = target.Namespace
		step.ProgramsOnly = component.ProgramsOnly()

		result.script.Components = append(result.script.Components, step)
	}

	for _, name := range hookBuilds {
		hook, ok := man.Hooks[name]
		if !ok {
			continue
		}

		step, reason, err := exportStep(backends, "the hook "+name, "hooks."+name,
			man.HookTarget(), hook, hook.Cwd, locked)
		if err != nil {
			return exported{}, err
		}

		if reason != "" {
			result.ttOnly = append(result.ttOnly, reason)

			continue
		}

		step.Hook = name

		if name == hookPreBuild {
			result.script.PreBuild = &step
		} else {
			result.script.PostBuild = &step
		}
	}

	return result, nil
}

// hookPreBuild is the hook a build runs before the builds of the components.
const hookPreBuild = "pre_build"

// exportStep asks the build of block, the build block at field of the
// manifest that builds target, what, in the working directory cwd, relative
// to the project, for its export, and returns the step of the script of it,
// but for what names the component or the hook. It returns why the build
// does not export instead, when it does not.
func exportStep(
	backends *buildreg.Registry, what, field string, target manifest.BuildTarget,
	block manifest.Build, cwd string, locked func(string) (string, bool),
) (rockscript.Step, string, error) {
	backend, bld, err := backends.Build(field, target, block)

	switch {
	case errors.Is(err, buildreg.ErrNoBackend):
		return rockscript.Step{}, fmt.Sprintf("%s is built by the tt backend %q, which no "+
			"module of this tt provides", what, block.Backend), nil
	case err != nil:
		return rockscript.Step{}, "", stateErrorf("%w", err)
	}

	exporter, ok := bld.(sdkbuild.Exporter)
	if !backend.Exports || !ok {
		return rockscript.Step{}, fmt.Sprintf("%s is built by the tt backend %q, which "+
			"builds with tt alone", what, backend.Name), nil
	}

	export, err := exporter.Export(&sdkbuild.ExportContext{
		Component: sdkbuild.Component{
			Name: target.Component, Path: target.Path, Namespace: target.Namespace,
			Package: target.Package, Wants: buildreg.Wants(target.Kind),
		},
		Hook:          target.Hook,
		LockedVersion: locked,
	})
	if errors.Is(err, sdkbuild.ErrNoExport) {
		return rockscript.Step{}, noExportReason(what, field, backend.Name, err), nil
	}

	if err != nil {
		return rockscript.Step{}, "", stateErrorf("%s: the export of the tt backend %q: %w",
			field, backend.Name, err)
	}

	rel, inProject := projectRel(cwd)
	if !inProject {
		return rockscript.Step{}, fmt.Sprintf("%s is built in %s, out of the project, which "+
			"the sources of the rock do not hold", what, cwd), nil
	}

	err = rockscript.CheckEnv(block.Env)
	if err != nil {
		return rockscript.Step{}, fmt.Sprintf("%s: %s.env: %v", what, field, err), nil
	}

	var outputs []sdkbuild.OutputFile

	if backend.OutputFrom == sdkbuild.OutputCwd && !target.Hook {
		outputs = buildreg.OutputFiles(backend, block.Output, target.Kind)
	}

	step := rockscript.Step{
		Component: "", Hook: "", Backend: backend.Name, Cwd: rel, Env: block.Env,
		Namespace: "", ProgramsOnly: false, Export: export, Outputs: outputs,
	}

	err = rockscript.CheckStep(step)
	if err != nil {
		return rockscript.Step{}, fmt.Sprintf("%s is built by the tt backend %q with tt "+
			"alone: %v", what, backend.Name, err), nil
	}

	return step, "", nil
}

// noExportReason says why the build of what, the block at field of the tt
// backend backend, builds with tt alone: err, its Export, wraps
// sdkbuild.ErrNoExport and names the field of the block, which the reason
// names from the manifest.
func noExportReason(what, field, backend string, err error) string {
	var fieldErr *sdkbuild.FieldError

	if !errors.As(err, &fieldErr) {
		return fmt.Sprintf("%s is built by the tt backend %q with tt alone: %v", what, backend,
			err)
	}

	where := field
	if fieldErr.Field != "" {
		where += "." + fieldErr.Field
	}

	return fmt.Sprintf("%s is built by the tt backend %q with tt alone, for %s: %s", what,
		backend, where, fieldErr.Msg)
}

// projectRel returns dir, a directory of the manifest relative to the root
// of the project, slash-separated and clean, and whether it is in the
// project.
func projectRel(dir string) (string, bool) {
	if filepath.IsAbs(dir) {
		return "", false
	}

	rel := path.Clean(filepath.ToSlash(dir))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}

	if rel == "." {
		rel = ""
	}

	return rel, true
}

// lockedVersions returns the versions lock pins for the closure of product
// and the dev closure, as a build answers sdkbuild.Context.LockedVersion: the
// first of either that pins a rock.
func lockedVersions(lock *manifest.Lock, product string) func(string) (string, bool) {
	pinned := map[string]string{}

	if lock != nil {
		for _, closure := range [][]manifest.LockDependency{
			lock.Products[product].Dependencies, lock.DevDependencies,
		} {
			for _, dep := range closure {
				if _, ok := pinned[dep.Name]; !ok {
					pinned[dep.Name] = dep.Version
				}
			}
		}
	}

	return func(rock string) (string, bool) {
		version, ok := pinned[rock]

		return version, ok
	}
}
