package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/context"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

// NewStartCmd creates start command.
func NewStartCmd() *cobra.Command {
	var startCmd = &cobra.Command{
		Use:   "start [APPLICATION_NAME]",
		Short: "Start tarantool instance",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&ctx, cmd.Name(), &modulesInfo, internalStartModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return startCmd
}

// internalStartModule is a default start module.
func internalStartModule(ctx *context.Ctx, args []string) error {
	cliOpts, err := modules.GetCliOpts(ctx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	if err = running.FillCtx(cliOpts, ctx, args); err != nil {
		return err
	}

	if err := running.Start(ctx); err != nil {
		return err
	}

	return nil
}
