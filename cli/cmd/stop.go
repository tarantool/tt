package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

// NewStopCmd creates stop command.
func NewStopCmd() *cobra.Command {
	var stopCmd = &cobra.Command{
		Use:   "stop <INSTANCE_NAME>",
		Short: "Stop tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalStopModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return stopCmd
}

// internalStopModule is a default stop module.
func internalStopModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	var runningCtx running.RunningCtx
	if err = running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	for _, run := range runningCtx.Instances {
		if err = running.Stop(&run); err != nil {
			log.Infof(err.Error())
		}
	}

	return nil
}
