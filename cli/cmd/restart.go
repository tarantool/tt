package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
)

// NewRestartCmd creates start command.
func NewRestartCmd() *cobra.Command {
	var restartCmd = &cobra.Command{
		Use:   "restart <INSTANCE_NAME>",
		Short: "Restart tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalRestartModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return restartCmd
}

// internalRestartModule is a default restart module.
func internalRestartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if err := internalStopModule(cmdCtx, args); err != nil {
		return err
	}

	if err := internalStartModule(cmdCtx, args); err != nil {
		return err
	}

	return nil
}
