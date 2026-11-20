package build

import (
	"fmt"

	"github.com/tarantool/tt/sdk/v3"
	sdkbuild "github.com/tarantool/tt/sdk/build/v3"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// ComponentContext returns the Context the build of the component named
// component of the product opts.Product selects would run in, and the build
// its backend makes of its block, with nothing built: for a command of a module that
// runs the tools of its own backend in the environment of a build, as tt
// wasm fetch and bind do. The manifest is read and checked as the build
// checks it, the version is derived, the lock is read from disk and not
// resolved - LockedVersion answers as during the preflight of a build - and
// TT_BUILD_DIR, Dirs.Work, is created; no hook, no resolution and no
// backend runs, and the directories of the stage and of the tree may not
// exist. opts.Args are the Args of the Context. A component the product
// does not build is errUnknownComponent, and one with no build block
// errNoBuild.
func ComponentContext(opts Options, component string) (*sdkbuild.Context, sdkbuild.Build, error) {
	man, err := readManifest(opts.ProjectDir, opts)
	if err != nil {
		return nil, nil, err
	}

	productName, product, err := selectProduct(man, opts.Product)
	if err != nil {
		return nil, nil, err
	}

	_, err = selectComponents(product, component)
	if err != nil {
		return nil, nil, sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	if man.Components[component].Build == nil {
		return nil, nil, sdk.Errorf(sdk.ExitFailure, "%w: %s", errNoBuild, component)
	}

	ver, err := version.Derive(opts.ProjectDir)
	if err != nil {
		return nil, nil, fmt.Errorf("deriving version: %w", err)
	}

	proj := packageProject(opts.ProjectDir, man, productName)

	proj.pinned = freshPins(opts.ProjectDir, man, productName)

	run, err := newBuilder(opts, proj, packageStage(opts.ProjectDir, productName),
		ver).component(component)
	if err != nil {
		return nil, nil, sdk.Errorf(sdk.ExitFailure, "component %q: %w", component, err)
	}

	err = prepareBuildDir(run.context.Dirs.Work, false)
	if err != nil {
		return nil, nil, sdk.Errorf(sdk.ExitSystem, "component %q: %w", component, err)
	}

	return run.context, run.build, nil
}
