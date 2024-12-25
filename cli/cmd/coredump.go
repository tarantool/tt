package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/coredump"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

var (
	coredumpPackExecutable      string
	coredumpPackPID             uint
	coredumpPackTime            string
	coredumpPackOutputDirectory string
	coredumpInspectSourceDir    string
)

// NewCoredumpCmd creates coredump command.
func NewCoredumpCmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "coredump",
		Short: "Perform manipulations with the tarantool coredumps",
	}

	var packCmd = &cobra.Command{
		Use:   "pack COREDUMP",
		Short: "pack tarantool coredump into tar.gz archive",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalCoredumpPackModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(1),
	}
	packCmd.Flags().StringVarP(&coredumpPackExecutable, "executable", "e", "",
		"Tarantool executable path")
	packCmd.Flags().StringVarP(&coredumpPackOutputDirectory, "directory", "d", "",
		"Directory the resulting archive is created in")
	packCmd.Flags().StringVarP(&coredumpPackTime, "time", "t", "",
		"Time of dump, expressed as seconds since the Epoch (1970-01-01 00:00 UTC)")
	packCmd.Flags().UintVarP(&coredumpPackPID, "pid", "p", 0,
		"PID of the dumped process, as seen in the PID namespace in which\n"+
			"the given process resides (see %p in core(5) for more info). This flag\n"+
			"is to be used when tt is used as kernel.core_pattern pipeline script")

	var unpackCmd = &cobra.Command{
		Use:   "unpack ARCHIVE",
		Short: "unpack tarantool coredump tar.gz archive",
		Run: func(cmd *cobra.Command, args []string) {
			if err := coredump.Unpack(args[0]); err != nil {
				util.HandleCmdErr(cmd, err)
			}
		},
		Args: cobra.ExactArgs(1),
	}

	var inspectCmd = &cobra.Command{
		Use:   "inspect {ARCHIVE|DIRECTORY}",
		Short: "inspect tarantool coredump",
		Run: func(cmd *cobra.Command, args []string) {
			if err := coredump.Inspect(args[0], coredumpInspectSourceDir); err != nil {
				util.HandleCmdErr(cmd, err)
			}
		},
		Args: cobra.ExactArgs(1),
	}
	inspectCmd.Flags().StringVarP(&coredumpInspectSourceDir, "sourcedir", "s", "",
		"Source directory")

	cmd.AddCommand(
		packCmd,
		unpackCmd,
		inspectCmd,
	)

	return cmd
}

// internalCoredumpPackModule is a default "pack" command for the coredump module.
func internalCoredumpPackModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	executable := coredumpPackExecutable
	if coredumpPackExecutable == "" {
		executable = cmdCtx.Cli.TarantoolCli.Executable
	}
	return coredump.Pack(args[0],
		executable,
		coredumpPackOutputDirectory,
		coredumpPackPID,
		coredumpPackTime,
	)
}
