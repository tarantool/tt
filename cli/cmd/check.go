package cmd

import (
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
)

// NewCheckCmd creates a new check command.
func NewCheckCmd() *cobra.Command {
	var checkCmd = &cobra.Command{
		Use:   "check [APPLICATION_NAME]",
		Short: "Check an application file for syntax errors",
		Run: func(cmd *cobra.Command, args []string) {
			err := modules.RunCmd(&cmdCtx, cmd.Name(), &modulesInfo, internalCheckModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
	}

	return checkCmd
}

// internalCheckModule is a default check module.
func internalCheckModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	cliOpts, err := modules.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	if err = running.FillCtx(cliOpts, cmdCtx, args); err != nil {
		return err
	}

	if err := running.Check(cmdCtx); err != nil {
		return err
	}
	log.Infof("Result of check: syntax of file '%s' is OK", cmdCtx.Running.AppPath)

	return nil
}
