package build

import (
	"context"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// runCommand implements the `command` build backend.
//
// Each of spec.Build.BuildCommand and spec.Build.InstallCommand, when
// non-empty, is executed verbatim via `sh -c <cmd>` from srcDir with the
// buildEnv(cfg) overlay applied to the inherited process environment.
//
// Either may be empty — the corresponding phase is then a no-op.
//
// This is intentionally minimal: upstream luarocks injects only CC into
// the env (cfg.variables.CC), but for Tarantool builds we expose the full
// canonical set so rockspec commands can `${LUA_INCDIR}` etc. just like
// other backends.
func runCommand(ctx context.Context, spec *rocks.Rockspec, srcDir, _ string, cfg rocks.Config) error {
	env := buildEnv(cfg)

	if spec.Build.BuildCommand != "" {
		err := runCmd(ctx, "sh", []string{"-c", spec.Build.BuildCommand}, srcDir, env)
		if err != nil {
			return err
		}
	}

	if spec.Build.InstallCommand != "" {
		err := runCmd(ctx, "sh", []string{"-c", spec.Build.InstallCommand}, srcDir, env)
		if err != nil {
			return err
		}
	}

	return nil
}
