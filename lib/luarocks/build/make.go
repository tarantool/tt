package build

import (
	"context"
	"maps"
	"sort"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// runMake implements the `make` build backend.
//
// Sequence (mirrors upstream luarocks/src/luarocks/build/make.lua):
//
//  1. make [<build_target>] K=V ...   (build phase)
//     - target defaults to "" (make's default target) if BuildTarget is unset
//     - K=V assignments come from spec.Build.BuildVariables, passed via env
//  2. make <install_target>           (install phase)
//     - target defaults to "install" if InstallTarget is unset
//     - K=V assignments come from spec.Build.InstallVariables, passed via env
//
// Working directory is srcDir for both phases. The base env is buildEnv(cfg)
// — the five canonical TARANTOOL_DIR/LUA_* vars — overlaid with the
// per-phase build/install variables. The phase variables WIN over buildEnv
// when both define the same key.
//
// We pass variables as env, not as `K=V` argv (upstream make.lua does
// both interchangeably; env is closer to how Makefiles consume them and
// it keeps the argv shape simple and shell-safe).
func runMake(ctx context.Context, spec *rocks.Rockspec, srcDir, _ string, cfg rocks.Config) error {
	base := buildEnv(cfg)

	// Build phase.
	buildArgs := []string{}
	if spec.Build.BuildTarget != "" {
		buildArgs = append(buildArgs, spec.Build.BuildTarget)
	}

	err := runCmd(ctx, "make", buildArgs, srcDir, overlay(base, spec.Build.BuildVariables))
	if err != nil {
		return err
	}

	// Install phase.
	installTarget := spec.Build.InstallTarget
	if installTarget == "" {
		installTarget = "install"
	}

	installArgs := []string{installTarget}

	return runCmd(ctx, "make", installArgs, srcDir, overlay(base, spec.Build.InstallVariables))
}

// overlay returns a copy of base with vars layered on top. base is left
// unchanged. Order is deterministic for tests (vars iterated in sorted
// key order, though map iteration order doesn't affect the final map).
func overlay(base, vars map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(vars))
	maps.Copy(out, base)

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		out[k] = vars[k]
	}

	return out
}
