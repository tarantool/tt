package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// NewStopCmd creates the stop command of tt instance.
func NewStopCmd() *cobra.Command {
	stopCmd := &cobra.Command{
		Use:   "stop " + instanceTargetsUsage,
		Short: "Stop instances",
		RunE:  internalRunE(internalStopWithConfirmationModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Active)
		},
	}

	addInstanceFlags(stopCmd)
	addYesFlag(stopCmd)

	return stopCmd
}

func internalStopWithConfirmationModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveProcesses(cmdCtx, args)
	if err != nil {
		return err
	}

	err = refuseManaged(instances, "stop", nil)
	if err != nil {
		return err
	}

	// --no-prompt is no confirmation: --yes is.
	confirmed, err := confirm("Confirm stop of " + describeTargets(args))
	if err != nil {
		return err
	}

	if !confirmed {
		log.Info("Stop is cancelled.")

		return nil
	}

	return stopInstances(instances)
}

// stopInstances interrupts all supervisors before collecting individual
// outcomes. An instance that is not running is no failure: it is said to be
// not running, as tt instance kill and logrotate say it.
func stopInstances(instances []running.InstanceCtx) error {
	result := newInstancesResult("stop", len(instances))

	for i, err := range running.StopAllSaying(instances) {
		if err != nil {
			result.fail(running.GetAppInstanceName(instances[i]), err)
		}
	}

	return result.err()
}
