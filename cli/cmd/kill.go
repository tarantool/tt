package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	forceKill bool
	dumpQuit  bool
)

// NewKillCmd creates kill command.
func NewKillCmd() *cobra.Command {
	killCmd := &cobra.Command{
		Use:   "kill " + instanceTargetsUsage,
		Short: "Kill tarantool instance(s)",
		RunE:  internalRunE(internalKillModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete,
				internal.Active)
		},
	}

	killCmd.Flags().BoolVarP(&forceKill, "force", "f", false, "do not ask for confirmation")
	killCmd.Flags().BoolVarP(&dumpQuit, "dump", "d", false, "quit with dump")

	return killCmd
}

func askConfirmation(args []string) (bool, error) {
	if forceKill {
		return true, nil
	}

	return util.AskConfirm(os.Stdin, "Kill "+killTargets(args)+"?")
}

// killTargets names what args, the targets of tt kill, kill for the prompt:
// all instances, the instances of an application or one instance.
func killTargets(args []string) string {
	targets, err := app.ParseTargets(args)

	switch {
	case err != nil || len(targets) > 1:
		return "instances " + strings.Join(args, ",")
	case len(targets) == 0:
		return "all instances"
	case targets[0].Instance == app.AllInstances:
		return "instances of " + targets[0].App
	default:
		return targets[0].String() + " instance"
	}
}

// internalKillModule is a kill module.
func internalKillModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	confirm, err := askConfirmation(args)
	if err != nil {
		return fmt.Errorf("error asking for confirmation: %w", err)
	}

	if !confirm {
		return nil
	}

	for _, run := range instances {
		if dumpQuit {
			err = running.Quit(run)
			if err != nil {
				log.Infof(err.Error())
			}
		} else {
			err = running.Kill(run)
			if err != nil {
				log.Infof(err.Error())
			}
		}
	}

	return nil
}
