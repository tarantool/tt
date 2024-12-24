package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
)

// NewLogrotateCmd creates logrotate command.
func NewLogrotateCmd() *cobra.Command {
	var logrotateCmd = &cobra.Command{
		Use:   "logrotate [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Rotate logs of a started tarantool instance(s)",
		Run:   TtModuleCmdRun(internalLogrotateModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractAppNames,
				running.ExtractInstanceNames)
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
	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, running.ConfigLoadSkip)
	if err != nil {
		return err
	}

	for _, run := range runningCtx.Instances {
		err := running.Logrotate(&run)
		if err != nil {
			log.Infof(err.Error())
		}
	}

	return nil
}
