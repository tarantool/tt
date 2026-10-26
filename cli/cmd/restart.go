package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/util"
)

var autoYes bool

// NewRestartCmd creates start command.
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
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete,
				internal.Active)
		},
	}

	restartCmd.Flags().BoolVarP(&autoYes, "yes", "y", false,
		`Automatic yes to confirmation prompt`)

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

	if !autoYes {
		confirmed, err := util.AskConfirm(os.Stdin, "Confirm restart of "+describeTargets(args))
		if err != nil {
			return err
		}

		if !confirmed {
			log.Info("Restart is cancelled.")

			return nil
		}
	}

	stopInstances(instances)

	err = internalStartModule(cmdCtx, args)
	if err != nil {
		return err
	}

	return nil
}
