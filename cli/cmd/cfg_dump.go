package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cfg"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

var rawDump bool

// NewDumpCmd creates a new dump command.
func NewDumpCmd() *cobra.Command {
	dumpCmd := &cobra.Command{
		Use:   "dump",
		Short: "Print environment configuration",
		RunE:  internalRunE(internalDumpModule),
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
