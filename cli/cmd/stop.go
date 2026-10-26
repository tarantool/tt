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
		Short: "Stop tarantool instance(s)",
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
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	if !cmdCtx.Cli.NoPrompt {
		confirmed, err := confirm("Confirm stop of " + describeTargets(args))
		if err != nil {
			return err
		}

		if !confirmed {
			log.Info("Stop is cancelled.")

			return nil
		}
	}

	stopInstances(instances)

	return nil
}

// stopInstances interrupts all instances before waiting, reporting failures.
func stopInstances(instances []running.InstanceCtx) {
	err := running.StopAll(instances)
	if err != nil {
		log.Infof("%s", err)
	}
}
