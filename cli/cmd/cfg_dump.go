package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cfg"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

var (
	rawDump bool
)

// NewDumpCmd creates a new dump command.
func NewDumpCmd() *cobra.Command {
	var dumpCmd = &cobra.Command{
		Use:   "dump",
		Short: "Print environment configuration",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalDumpModule, args)
			util.HandleCmdErr(cmd, err)
		},
	}

	dumpCmd.Flags().BoolVarP(&rawDump, "raw", "r", false,
		"Display the raw contents of tt environment config.")

	return dumpCmd
}

// internalDumpModule is a default dump module.
func internalDumpModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	dumpCtx := cfg.DumpCtx{
		RawDump: rawDump,
	}

	return cfg.RunDump(os.Stdout, cmdCtx, &dumpCtx, cliOpts)
}
