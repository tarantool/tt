package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

var forceKill bool
var dumpQuit bool

// NewKillCmd creates kill command.
func NewKillCmd() *cobra.Command {
	var killCmd = &cobra.Command{
		Use:   "kill [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Kill tarantool instance(s)",
		Run:   RunModuleFunc(internalKillModule),
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

	killCmd.Flags().BoolVarP(&forceKill, "force", "f", false, "do not ask for confirmation")
	killCmd.Flags().BoolVarP(&dumpQuit, "dump", "d", false, "quit with dump")

	return killCmd
}

func askConfirmation(args []string) (bool, error) {
	var err error
	var confirm bool = true
	if !forceKill {
		confirmationMsg := "Kill all instances?"
		if len(args) > 0 {
			if strings.ContainsRune(args[0], running.InstanceDelimiter) {
				confirmationMsg = fmt.Sprintf("Kill %s instance?", args[0])
			} else {
				confirmationMsg = fmt.Sprintf("Kill instances of %s?", args[0])
			}
		}
		confirm, err = util.AskConfirm(os.Stdin, confirmationMsg)
		if err != nil {
			return confirm, err
		}
	}
	return confirm, nil
}

// internalKillModule is a kill module.
func internalKillModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	confirm, err := askConfirmation(args)
	if err != nil {
		return fmt.Errorf("error asking for confirmation: %s", err)
	}

	if confirm {
		var runningCtx running.RunningCtx
		err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, running.ConfigLoadSkip)
		if err != nil {
			return err
		}

		for _, run := range runningCtx.Instances {
			if dumpQuit {
				if err = running.Quit(run); err != nil {
					log.Infof(err.Error())
				}
			} else {
				if err = running.Kill(run); err != nil {
					log.Infof(err.Error())
				}
			}
		}
	}

	return nil
}
