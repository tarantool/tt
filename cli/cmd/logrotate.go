package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// NewLogrotateCmd creates the logrotate command of tt instance.
func NewLogrotateCmd() *cobra.Command {
	logrotateCmd := &cobra.Command{
		Use:   "logrotate " + instanceTargetsUsage,
		Short: "Rotate logs of a started tarantool instance(s)",
		RunE:  internalRunE(internalLogrotateModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Any)
		},
	}

	addInstanceFlags(logrotateCmd)

	return logrotateCmd
}

// internalLogrotateModule is a default logrotate module.
func internalLogrotateModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	for _, run := range instances {
		err := running.Logrotate(&run)
		if err != nil {
			log.Infof(err.Error())
		}
	}

	return nil
}
