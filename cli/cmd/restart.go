package cmd

import (
	"fmt"
	"os"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

var autoYes bool

// NewRestartCmd creates start command.
func NewRestartCmd() *cobra.Command {
	restartCmd := &cobra.Command{
		Use:   "restart [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Restart tarantool instance(s)",
		Run:   RunModuleFunc(internalRestartModule),
		Args:  cobra.RangeArgs(0, 1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractActiveAppNames,
				running.ExtractActiveInstanceNames)
		},
	}

	restartCmd.Flags().BoolVarP(&autoYes, "yes", "y", false,
		`Automatic yes to confirmation prompt`)

	return restartCmd
}

// internalRestartModule is a default restart module.
func internalRestartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	if cmdCtx.Cli.TarantoolCli.Executable == "" {
		return fmt.Errorf("tarantool binary is not found")
	}

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
