package cmd

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// dumpQuit is --dump of tt instance kill: quit with a dump instead.
var dumpQuit bool

// NewKillCmd creates the kill command of tt instance.
func NewKillCmd() *cobra.Command {
	killCmd := &cobra.Command{
		Use:   "kill " + instanceTargetsUsage,
		Short: "Kill instances at once, without a graceful stop",
		Long: "Kill the instances the targets select with SIGKILL: the process group " +
			"of the watchdog of each instance, the instance included, and the process " +
			"group of an external process. Nothing stops gracefully, nothing is " +
			"restarted, and the pid file and the console socket are removed after " +
			"them. tt instance stop is the graceful stop; kill is for an instance that " +
			"does not stop.\n\n" +
			"With --dump tt sends SIGQUIT instead, which has Tarantool quit with a " +
			"core dump for a post-mortem, and waits for it to end. An external " +
			"process and an instance that is not running are refused.\n\n" +
			"kill asks for a confirmation, and off a terminal needs --yes. An " +
			"instance a systemd unit manages is refused: the error names the " +
			"systemctl kill command for its unit.",
		RunE: internalRunE(internalKillModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Active)
		},
	}

	addInstanceFlags(killCmd)
	addYesFlag(killCmd)
	killCmd.Flags().BoolVarP(&dumpQuit, "dump", "d", false,
		"quit with a core dump (SIGQUIT) instead of SIGKILL")

	return killCmd
}

// killTargets names what args, the targets of tt instance kill, kill for the
// prompt: all instances, the instances of an application or one instance.
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
	instances, err := resolveProcesses(cmdCtx, args)
	if err != nil {
		return err
	}

	// systemctl kill sends the signal to every process of the unit, the
	// instance among them: SIGKILL kills it as tt instance kill does, SIGQUIT
	// has it quit with a dump as --dump does. systemd then restarts it as
	// Restart= says.
	signal := "SIGKILL"
	if dumpQuit {
		signal = "SIGQUIT"
	}

	err = refuseManaged(instances, "kill --signal="+signal, nil)
	if err != nil {
		return err
	}

	confirmed, err := confirm("Kill " + killTargets(args) + "?")
	if err != nil {
		return err
	}

	if !confirmed {
		return nil
	}

	if dumpQuit {
		return actOnInstances("kill", instances, func(inst *running.InstanceCtx) error {
			return running.Quit(*inst)
		})
	}

	return actOnInstances("kill", instances, func(inst *running.InstanceCtx) error {
		return running.Kill(*inst)
	})
}
