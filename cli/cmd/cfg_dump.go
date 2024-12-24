package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cfg"
	"github.com/tarantool/tt/cli/cmdcontext"
)

var (
	rawDump bool
)

// NewDumpCmd creates a new dump command.
func NewDumpCmd() *cobra.Command {
	var dumpCmd = &cobra.Command{
		Use:   "dump",
		Short: "Print environment configuration",
		Run:   TtModuleCmdRun(internalDumpModule),
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
