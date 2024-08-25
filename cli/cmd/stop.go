package cmd

import (
	"fmt"
	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"os"
)

// NewStopCmd creates stop command.
func NewStopCmd() *cobra.Command {
	var stopCmd = &cobra.Command{
		Use:   "stop [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Stop tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalStopWithConfirmationModule, args)
			util.HandleCmdErr(cmd, err)
		},
		Args: cobra.RangeArgs(0, 1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractActiveAppNames,
				running.ExtractActiveInstanceNames)
		},
	}

	stopCmd.Flags().BoolVarP(&autoYes, "yes", "y", false,
		`Automatic yes to confirmation prompt`)

	return stopCmd
}

func internalStopWithConfirmationModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	if !(autoYes || cmdCtx.Cli.NoPrompt) {
		instancesToConfirm := ""
		if len(args) == 0 {
			instancesToConfirm = "all instances"
		} else {
			instancesToConfirm = fmt.Sprintf("'%s'", args[0])
		}
		confirmed, err := util.AskConfirm(os.Stdin, fmt.Sprintf("Confirm stop of %s",
			instancesToConfirm))
		if err != nil {
			return err
		}
		if !confirmed {
			log.Info("Stop is cancelled.")
			return nil
		}
	}

	if err := internalStopModule(cmdCtx, args); err != nil {
		return err
	}

	return nil
}

// internalStopModule is a default stop module.
func internalStopModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var runningCtx running.RunningCtx
	var err error
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
