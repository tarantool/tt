package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// NewRestartCmd creates the restart command of tt instance.
func NewRestartCmd() *cobra.Command {
	restartCmd := &cobra.Command{
		Use:   "restart " + instanceTargetsUsage,
		Short: "Restart instance(s)",
		RunE:  internalRunE(internalRestartModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Active)
		},
	}

	addInstanceFlags(restartCmd)
	addYesFlag(restartCmd)

	return restartCmd
}

// internalRestartModule is a default restart module: it stops the instances
// and starts them again in the background, resolving their applications
// anew, so that an installed application restarts on its active version.
//
// Every target is resolved and checked as tt instance start checks it, the
// pair, the tarantool and the layout, before any of them stops: a typo in
// the description must not take running instances down, so a target that
// cannot start fails the command before it acts, and the error names it.
// So does a target that a systemd unit manages.
//
// After that an instance that does not stop is not started again, and does
// not keep the others from restarting; the outcome counts the instances
// that failed to stop or to start (instancesResult).
func internalRestartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	err = refuseManaged(instances, "restart", nil)
	if err != nil {
		return err
	}

	confirmed, err := confirm("Confirm restart of " + describeTargets(args))
	if err != nil {
		return err
	}

	if !confirmed {
		log.Info("Restart is cancelled.")

		return nil
	}

	canStart, err := running.IsAbleToStartInstances(instances)
	if !canStart {
		return err
	}

	result := newInstancesResult("restart", len(instances))
	stopped := make([]running.InstanceCtx, 0, len(instances))

	// Every watchdog is interrupted before any is waited for, so that the
	// instances shut down together (running.StopAllResults).
	for index, err := range running.StopAllResults(instances) {
		if err != nil {
			result.fail(running.GetAppInstanceName(instances[index]), err)

			continue
		}

		stopped = append(stopped, instances[index])
	}

	if len(stopped) > 0 {
		startWatchdogs(cmdCtx, stopped, result)
	}

	return result.err()
}
