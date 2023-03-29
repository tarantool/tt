package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

// NewLogrotateCmd creates logrotate command.
func NewLogrotateCmd() *cobra.Command {
	var logrotateCmd = &cobra.Command{
		Use:   "logrotate [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Rotate logs of a started tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalLogrotateModule, args)
			handleCmdErr(cmd, err)
		},
	}

	return logrotateCmd
}

// internalLogrotateModule is a default logrotate module.
func internalLogrotateModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var runningCtx running.RunningCtx
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	for _, run := range runningCtx.Instances {
		res, err := running.Logrotate(&run)
		if err != nil {
			return err
		}
		log.Info(res)
	}

	return nil
}
