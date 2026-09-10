package cmd

import (
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/manifest/run"
)

// NewRunCmd creates `tt run`: the project's Tarantool, with the arguments
// passed straight through.
func NewRunCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run [ARGS...]",
		Short: "Run the current package under its Tarantool",
		Long: `Run the package in the current directory under Tarantool.

The directory must hold app.manifest.toml; no tt environment and no tt.yaml are
involved. The interpreter is the one the package bundles under _runtime/ when it
has one, and the host's tarantool otherwise — TT_USE_SYSTEM_TARANTOOL=1 forces
the host's either way.

Every argument is handed to Tarantool untouched, '--' included, and the
environment is inherited unchanged: no LUA_PATH or LUA_CPATH is composed, since
Tarantool finds .rocks/ from the project directory by itself. tt replaces itself
with the interpreter, so signals and the exit code are Tarantool's own.
`,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			for _, opt := range args {
				if opt == "-h" || opt == "--help" {
					_ = cmd.Help()

					return
				}
			}

			RunModuleFunc(internalRunModule)(cmd, args)
		},
		Example: `
# Start an interactive console in the project.

    $ tt run

# Print the version of the Tarantool the project runs under:

    $ tt run --version
    Tarantool 3.0.0-entrypoint-724-gd2d7f4de3
    . . .

# Run a script with arguments. Everything after the script name is the
# script's, '--' included, because Tarantool itself is what reads them:

    $ tt run script.lua a b c
    a	b	c

# Execute stdin:

    $ echo 'print(42)' | tt run -

# Ignore the bundled runtime and use the host's tarantool:

    $ TT_USE_SYSTEM_TARANTOOL=1 tt run script.lua
`,
		DisableFlagsInUseLine: true,
	}

	return runCmd
}

// internalRunModule is a default run module.
func internalRunModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	root, tarantool, err := selectProjectTarantool(cmdCtx)
	if err != nil {
		return err
	}

	return run.Exec(root, tarantool, args)
}

// selectProjectTarantool resolves the project root and the interpreter it runs
// under, shared by tt run and tt test.
//
// cmdCtx supplies the tt environment's own answer, which is how bin_dir keeps
// working for a project inside a tt environment; it is empty when tt resolved
// none, and the selection then falls back to PATH.
func selectProjectTarantool(cmdCtx *cmdcontext.CmdCtx) (string, string, error) {
	workingDir, err := absoluteWorkingDir()
	if err != nil {
		return "", "", err
	}

	root, err := run.ProjectRoot(workingDir)
	if err != nil {
		return "", "", err
	}

	tarantool, err := run.SelectTarantool(root, run.Environment{
		Executable: cmdCtx.Cli.TarantoolCli.Executable,
		UseSystem:  run.UseSystemFromEnv(),
	})
	if err != nil {
		return "", "", err
	}

	return root, tarantool, nil
}
