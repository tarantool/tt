package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/binary"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
)

// NewBinariesCmd creates binaries command.
func NewBinariesCmd() *cobra.Command {
	var binariesCmd = &cobra.Command{
		Use: "binaries",
	}

	var listCmd = &cobra.Command{
		Use:   "list",
		Short: "Show a list of installed binaries and their versions.",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalListModule, args)
			handleCmdErr(cmd, err)
		},
	}

	binariesCmd.AddCommand(listCmd)
	return binariesCmd
}

// internalListModule is a list module.
func internalListModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	return binary.ListBinaries(cmdCtx, cliOpts)
}
