package build

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/tarantool/tt/cli/manifest"
	"github.com/tarantool/tt/cli/manifest/build/backend"
	"github.com/tarantool/tt/cli/manifest/version"
)

// Hook names, matching the manifest's [hooks.<name>] tables.
const (
	hookPreBuild  = "pre_build"
	hookPostBuild = "post_build"
)

// runHook runs the named lifecycle hook if the manifest declares it, under the
// reduced hook contract (TT_PACKAGE, TT_VERSION and friends, but no per-
// component TT_OUTPUT_DIR / TT_COMPONENT_NAME). An absent hook is a no-op. The
// hook runs in its own cwd (default: the project root), so pre_build can
// generate files anywhere in the tree before components are gathered.
func runHook(
	ctx context.Context, man *manifest.Manifest, ver version.Version,
	name, projectDir string, showOutput bool,
) error {
	hook, ok := man.Hooks[name]
	if !ok {
		return nil
	}

	env := backend.Env{
		OutputDir:   "",
		ProjectRoot: projectDir,
		Package:     man.Package.Name,
		Component:   "",
		Version:     ver.SemVer,
		OS:          "",
		Arch:        "",
		Extra:       hook.Env,
	}

	cwd := resolveDir(projectDir, hook.Cwd)

	err := backend.RunHook(ctx, hook, cwd, env, showOutput)
	if err != nil {
		return fmt.Errorf("%s hook: %w", name, err)
	}

	return nil
}

// resolveDir resolves a manifest-supplied directory against the project root: an
// empty value defaults to the root, an absolute value is used verbatim, and a
// relative value is joined onto the root. The result is always absolute (the
// backend contract requires it) as long as projectDir is.
func resolveDir(projectDir, dir string) string {
	switch {
	case dir == "":
		return projectDir
	case filepath.IsAbs(dir):
		return dir
	default:
		return filepath.Join(projectDir, dir)
	}
}
