package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/coredump"
)

// NewCoredumpCmd creates coredump command.
func NewCoredumpCmd() *cobra.Command {
	var coredumpCmd = &cobra.Command{
		Use:   "coredump",
		Short: "Perform manipulations with the tarantool coredumps",
	}

	var packCmd = &cobra.Command{
		Use:   "pack COREDUMP",
		Short: "pack tarantool coredump into tar.gz archive",
		Run: func(cmd *cobra.Command, args []string) {
			if err := coredump.Pack(args[0]); err != nil {
				handleCmdErr(cmd, err)
			}
		},
		Args: cobra.ExactArgs(1),
	}

	var unpackCmd = &cobra.Command{
		Use:   "unpack ARCHIVE",
		Short: "unpack tarantool coredump tar.gz archive",
		Run: func(cmd *cobra.Command, args []string) {
			if err := coredump.Unpack(args[0]); err != nil {
				handleCmdErr(cmd, err)
			}
		},
		Args: cobra.ExactArgs(1),
	}

	var sourceDir string
	var inspectCmd = &cobra.Command{
		Use:   "inspect {ARCHIVE|DIRECTORY}",
		Short: "inspect tarantool coredump",
		Run: func(cmd *cobra.Command, args []string) {
			if err := coredump.Inspect(args[0], sourceDir); err != nil {
				handleCmdErr(cmd, err)
			}
		},
		Args: cobra.ExactArgs(1),
	}
	inspectCmd.Flags().StringVarP(&sourceDir, "sourcedir", "s", "",
		"Source directory")

	subCommands := []*cobra.Command{
		packCmd,
		unpackCmd,
		inspectCmd,
	}

	for _, cmd := range subCommands {
		coredumpCmd.AddCommand(cmd)
	}

	return coredumpCmd
}
