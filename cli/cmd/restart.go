package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/context"
	"github.com/tarantool/tt/cli/modules"
)

// NewRestartCmd creates start command.
func NewRestartCmd() *cobra.Command {
	var restartCmd = &cobra.Command{
		Use:   "restart [INSTANCE_NAME]",
		Short: "restart tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&ctx, cmd.Name(), &modulesInfo, internalRestartModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return restartCmd
}

// internalRestartModule is a default restart module.
func internalRestartModule(ctx *context.Ctx, args []string) error {
	if err := internalStopModule(ctx, args); err != nil {
		return err
	}

	if err := internalStartModule(ctx, args); err != nil {
		return err
	}

	return nil
}
