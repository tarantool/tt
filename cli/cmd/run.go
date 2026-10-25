package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/manifest/run"
)

// NewRunCmd creates `tt run`: the project's Tarantool, with the arguments
// passed straight through.
func NewRunCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run [ARGS...]",
		Short: "Run the current package under its Tarantool",
		Long: `Run the package in the current directory under Tarantool.

The directory must hold app.manifest.toml; no tt environment and no tt.yaml are
involved. The interpreter is the project's own, the one .tarantool/bin/tarantool
in the directory selects, and the first tarantool on PATH when there is none —
TT_USE_SYSTEM_TARANTOOL=1 forces PATH either way. tt binary install and
tt binary switch set that link; a with-deps tt package install sets it to the
runtime the package bundles when the project selects no tarantool yet.

Every argument is handed to Tarantool untouched, '--' included, and the
environment is inherited unchanged: no LUA_PATH or LUA_CPATH is composed, since
Tarantool finds .rocks/ from the project directory by itself. tt replaces itself
with the interpreter, so signals and the exit code are Tarantool's own.
`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, opt := range args {
				if opt == "-h" || opt == "--help" {
					_ = cmd.Help()

					return nil
				}
			}

			return internalRunE(internalRunModule)(cmd, args)
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

# Ignore the project's tarantool and use the one on PATH:

    $ TT_USE_SYSTEM_TARANTOOL=1 tt run script.lua
`,
		DisableFlagsInUseLine: true,
	}

	return runCmd
}

// internalRunModule is a default run module.
func internalRunModule(_ *cmdcontext.CmdCtx, args []string) error {
	root, tarantool, err := selectProjectTarantool()
	if err != nil {
		return err
	}

	return run.Exec(root, tarantool, args)
}

// selectProjectTarantool resolves the project root and the interpreter it runs
// under, shared by tt run and tt test.
//
// The interpreter is chosen by the rule the instance commands follow for a
// project (cli/tarantool), so a package runs on the same tarantool under
// tt run as under tt instance start. bin_dir of tt.yaml plays no part in it.
// What the choice warns about, a project tarantool other than the one the
// lock bundles say, goes to the log.
func selectProjectTarantool() (string, string, error) {
	workingDir, err := absoluteWorkingDir()
	if err != nil {
		return "", "", err
	}

	root, err := run.ProjectRoot(workingDir)
	if err != nil {
		return "", "", err
	}

	exe, err := run.Tarantool(root)
	if err != nil {
		return "", "", err
	}

	for _, warning := range exe.Warnings {
		log.Warn(warning)
	}

	return root, exe.Path, nil
}
