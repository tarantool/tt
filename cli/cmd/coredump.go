package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/coredump"
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
	cmd := &cobra.Command{
		Use: "coredump",
		// spell-checker:ignore coredumps
		Short: "Perform manipulations with the tarantool coredumps",
	}

	packCmd := &cobra.Command{
		Use:   "pack COREDUMP",
		Short: "pack tarantool coredump into tar.gz archive",
		Run:   RunModuleFunc(internalCoredumpPackModule),
		Args:  cobra.ExactArgs(1),
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

	unpackCmd := &cobra.Command{
		Use:   "unpack ARCHIVE",
		Short: "unpack tarantool coredump tar.gz archive",
		Run:   RunModuleFunc(internalCoredumpUnpackModule),
		Args:  cobra.ExactArgs(1),
	}

	inspectCmd := &cobra.Command{
		Use:   "inspect {ARCHIVE|DIRECTORY}",
		Short: "inspect tarantool coredump",
		Run:   RunModuleFunc(internalCoredumpInspectModule),
		Args:  cobra.ExactArgs(1),
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

// internalCoredumpUnpackModule is a default "unpack" command for the coredump module.
func internalCoredumpUnpackModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	return coredump.Unpack(args[0])
}

// internalCoredumpInspectModule is a default "inspect" command for the coredump module.
func internalCoredumpInspectModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	return coredump.Inspect(args[0], coredumpInspectSourceDir)
}
