package cmd

import (
	"github.com/apex/log"
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
			if len(args) != 1 {
				log.Fatalf("Wrong number of arguments, please specify COREDUMP")
			}
			if err := runCoredumpCommand(coredump.Pack, args[0]); err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	var unpackCmd = &cobra.Command{
		Use:   "unpack ARCHIVE",
		Short: "unpack tarantool coredump tar.gz archive",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				log.Fatalf("Wrong number of arguments, please specify ARCHIVE")
			}
			if err := runCoredumpCommand(coredump.Unpack, args[0]); err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	var inspectCmd = &cobra.Command{
		Use:   "inspect FOLDER",
		Short: "inspect tarantool coredump folder",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				log.Fatalf("Wrong number of arguments, please specify FOLDER")
			}
			if err := runCoredumpCommand(coredump.Inspect, args[0]); err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	replicasetsSubCommands := []*cobra.Command{
		packCmd,
		unpackCmd,
		inspectCmd,
	}

	for _, cmd := range replicasetsSubCommands {
		coredumpCmd.AddCommand(cmd)
	}

	return coredumpCmd
}

// runCoredumpCommand is a default coredump module.
func runCoredumpCommand(replicasetsFunc func(args string) error, args string) error {

	if err := replicasetsFunc(args); err != nil {
		return err
	}

	return nil
}
