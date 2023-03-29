package cmd

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/util"
)

var (
	autoYes bool
)

// NewRestartCmd creates start command.
func NewRestartCmd() *cobra.Command {
	var restartCmd = &cobra.Command{
		Use:   "restart [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Restart tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalRestartModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.RangeArgs(0, 1),
	}

	restartCmd.Flags().BoolVarP(&autoYes, "yes", "y", false,
		`Automatic yes to confirmation prompt`)

	return restartCmd
}

// internalRestartModule is a default restart module.
func internalRestartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !autoYes {
		instancesToConfirm := ""
		if len(args) == 0 {
			instancesToConfirm = "all instances"
		} else {
			instancesToConfirm = fmt.Sprintf("'%s'", args[0])
		}
		confirmed, err := util.AskConfirm(os.Stdin, fmt.Sprintf("Confirm restart of %s",
			instancesToConfirm))
		if err != nil {
			return err
		}
		if !confirmed {
			log.Info("Restart is cancelled.")
			return nil
		}
	}

	if err := internalStopModule(cmdCtx, args); err != nil {
		return err
	}

	if err := internalStartModule(cmdCtx, args); err != nil {
		return err
	}

	return nil
}
