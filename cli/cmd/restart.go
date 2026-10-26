package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// NewRestartCmd creates the restart command of tt instance.
func NewRestartCmd() *cobra.Command {
	restartCmd := &cobra.Command{
		Use:   "restart " + instanceTargetsUsage,
		Short: "Restart tarantool instance(s)",
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
// and starts them again, resolving their applications anew, so that an
// installed application restarts on its active version.
func internalRestartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
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

	stopInstances(instances)

	return internalStartModule(cmdCtx, args)
}
