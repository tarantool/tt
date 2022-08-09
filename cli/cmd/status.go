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
		Use:   "status <INSTANCE_NAME>",
		Short: "Status of the tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalStatusModule, args)
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

	if err = running.FillCtx(cliOpts, cmdCtx, args); err != nil {
		return err
	}

	for _, run := range cmdCtx.Running {
		log.Infof("%s: %s", run.InstName, running.Status(cmdCtx, &run))
	}

	return nil
}
