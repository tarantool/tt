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
		Use:   "restart <INSTANCE_NAME>",
		Short: "Restart tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalRestartModule, args)
			if err != nil {
				log.Fatalf(err.Error())
			}
		},
		Args: cobra.ExactArgs(1),
	}

	restartCmd.Flags().BoolVarP(&autoYes, "yes", "y", false,
		`Automatic yes to confirmation prompt`)

	return restartCmd
}

// internalRestartModule is a default restart module.
func internalRestartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("restart accepts only 1 arg, received %d", len(args))
	}

	if !autoYes {
		confirmed, err := util.AskConfirm(os.Stdin, fmt.Sprintf("Confirm restart of '%s'", args[0]))
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
