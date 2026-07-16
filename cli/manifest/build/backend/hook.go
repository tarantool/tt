package backend

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/tarantool/tt/cli/manifest"
)

var (
	// errRelativeCwd reports a non-absolute hook working directory (rejected for
	// the same double-application reason as a component build).
	errRelativeCwd = errors.New("cwd must be an absolute path")
	// errUnrunnableHookBackend reports a hook whose backend is neither shell nor
	// make; the closed enum is enforced at manifest parse time.
	errUnrunnableHookBackend = errors.New(
		"hook backend is not runnable (only shell and make)")
)

// RunHook runs a lifecycle hook (pre_build / post_build) through the shell or
// make backend. It reuses the component build machinery — the same argv
// construction and the same last-wins environment assembly — but under the
// reduced hook contract: TT_OUTPUT_DIR and TT_COMPONENT_NAME are not set (a
// hook runs around all components, not inside one) and b.Output is ignored (a
// hook has no single output directory to copy into).
//
// Only shell and make are valid hook backends; the closed enum is enforced at
// manifest parse time (cli/manifest/validate.go), and any other backend here is
// a programming error surfaced as a plain error rather than a panic. cwd must be
// absolute for the same double-application reason as a component build.
func RunHook(
	ctx context.Context, hook manifest.Build, cwd string, env Env, showOutput bool,
) error {
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("%w, got %q", errRelativeCwd, cwd)
	}

	switch hook.Backend {
	case BackendShell:
		return runEnviron(ctx, cwd, env.hookEnviron(), showOutput, hook.Command, hook.Args...)
	case BackendMake:
		return runEnviron(ctx, cwd, env.hookEnviron(), showOutput, "make", makeArgs(hook, cwd)...)
	default:
		return fmt.Errorf("%w: %q", errUnrunnableHookBackend, hook.Backend)
	}
}
