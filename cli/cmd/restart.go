package cmd

import (
	"slices"

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
// An instance that does not stop is not started again, and does not keep
// the others from restarting; the outcome counts the instances that failed
// to stop or to start (instancesResult).
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

	result := newInstancesResult("restart", len(instances))

	var stopped []string

	for i := range instances {
		name := running.GetAppInstanceName(instances[i])

		err := running.Stop(&instances[i])
		if err != nil {
			result.fail(name, err)

			continue
		}

		stopped = append(stopped, name)
	}

	if len(stopped) > 0 {
		startStopped(cmdCtx, args, stopped, result)
	}

	return result.err()
}

// startStopped starts again the instances of args that restart stopped,
// named by stopped, resolving them anew, and records in result those that
// fail to start. A failure before any of them starts, such as a pair broken
// in the meantime, fails them all.
func startStopped(cmdCtx *cmdcontext.CmdCtx, args, stopped []string, result *instancesResult) {
	opts, err := startOptions(cmdCtx)
	if err != nil {
		result.failAll(stopped, err)

		return
	}

	instances, err := resolveInstancesWith(args, opts)
	if err != nil {
		result.failAll(stopped, err)

		return
	}

	instances = slices.DeleteFunc(instances, func(inst running.InstanceCtx) bool {
		return !slices.Contains(stopped, running.GetAppInstanceName(inst))
	})

	canStart, err := running.IsAbleToStartInstances(instances)
	if !canStart {
		result.failAll(stopped, err)

		return
	}

	startWatchdogs(cmdCtx, instances, result)
}
