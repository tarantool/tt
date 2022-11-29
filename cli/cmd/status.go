package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

// NewStatusCmd creates status command.
func NewStatusCmd() *cobra.Command {
	var statusCmd = &cobra.Command{
		Use:   "status [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Status of the tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalStatusModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return statusCmd
}

// internalStatusModule is a default status module.
func internalStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	var runningCtx running.RunningCtx
	if err = running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	for _, run := range runningCtx.Instances {
		fullInstanceName := running.GetAppInstanceName(run)
		procStatus := running.Status(&run)
		log.Infof("%s: %s", procStatus.ColorSprint(fullInstanceName), procStatus.Text)
	}

	return nil
}
