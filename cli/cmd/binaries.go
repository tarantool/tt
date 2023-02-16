package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/list"
	"github.com/tarantool/tt/cli/modules"
)

// NewBinariesCmd creates binaries command.
func NewBinariesCmd() *cobra.Command {
	var binariesCmd = &cobra.Command{
		Use:   "binaries",
		Short: "Show a list of installed binaries and their versions.",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalBinariesModule, args)
			handleCmdErr(cmd, err)
		},
	}

	return binariesCmd
}

// internalBinariesModule is a default binaries module.
func internalBinariesModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	err := list.ListBinaries(cmdCtx, cliOpts)

	return err
}
