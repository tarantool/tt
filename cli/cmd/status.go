package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/context"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

// NewStopCmd creates status command.
func NewStatusCmd() *cobra.Command {
	var statusCmd = &cobra.Command{
		Use:   "status [INSTANCE_NAME]",
		Short: "Status of the tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&ctx, cmd.Name(), &modulesInfo, internalStatusModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return statusCmd
}

// internalStatusModule is a default status module.
func internalStatusModule(ctx *context.Ctx, args []string) error {
	cliOpts, err := modules.GetCliOpts(ctx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	if err = running.FillCtx(cliOpts, ctx, args); err != nil {
		return err
	}

	log.Info(running.Status(ctx))

	return nil
}
