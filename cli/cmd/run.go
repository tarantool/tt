package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
)

func newRunInfo(cmdCtx cmdcontext.CmdCtx) *running.RunInfo {
	return &running.RunInfo{
		CmdCtx: cmdCtx,
	}
}

// NewRunCmd creates run command.
func NewRunCmd() *cobra.Command {
	runCmd := &cobra.Command{
		Use:   "run [SCRIPT.lua [flags] [-- ARGS]]",
		Short: "Run Tarantool instance",
		Long: `Run Tarantool instance.
All command line arguments are passed to the interpreted SCRIPT. Options to process in the SCRIPT
are passed after '--'.
`,
		DisableFlagParsing: true,
		Run: func(cmd *cobra.Command, args []string) {
			for _, opt := range args {
				if opt == "-h" || opt == "--help" {
					cmd.Help()
					return
				}
			}
			RunModuleFunc(internalRunModule)(cmd, args)
		},
		Example: `
# Print current environment Tarantool version:

    $ tt run --version
    Tarantool 2.11.0-entrypoint-724-gd2d7f4de3
    . . .

# Run a script (which print passed arguments) with 3 arguments and 2 options:

    $ tt run script.lua a b c -- -a -b
    a	b	c	-a	-b

# Run a script, pass '-i' argument to it, and enter interactive mode after script execution:

    $ tt run -i script.lua -- -i
    -i
    Tarantool 2.11.0-entrypoint-724-gd2d7f4de3
    type 'help' for interactive help
    tarantool>

First '-i' option is parsed by 'tt run' and means 'enter interactive mode'. The second '-i'
is after '--', so passed to script.lua as is.

# Execute stdin:

    $ echo 'print(42)' | tt run -
    42

`,
		DisableFlagsInUseLine: true,
	}

	return runCmd
}

// internalRunModule is a default run module.
func internalRunModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	runInfo := newRunInfo(*cmdCtx)

	runInfo.RunOpts.RunArgs = args

	if err := running.Run(runInfo); err != nil {
		return err
	}

	return nil
}
