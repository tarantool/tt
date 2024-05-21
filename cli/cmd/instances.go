package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/instances"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

// NewInstancesCmd creates instances command.
func NewInstancesCmd() *cobra.Command {
	var instancesCmd = &cobra.Command{
		Use:   "instances",
		Short: "Show list of enabled applications",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalInstancesModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.ExactArgs(0),
	}

	return instancesCmd
}

// internalInstancesModule is a default instances module.
func internalInstancesModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	err := instances.ListInstances(cmdCtx, cliOpts)

	return err
}
