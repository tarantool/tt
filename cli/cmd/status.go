package cmd

import (
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/status"
	"github.com/tarantool/tt/cli/util"
)

var opts status.StatusOpts

// NewStatusCmd creates status command.
func NewStatusCmd() *cobra.Command {
	var statusCmd = &cobra.Command{
		Use:   "status [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Status of the tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalStatusModule, args)
			util.HandleCmdErr(cmd, err)
		},
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractAppNames,
				running.ExtractInstanceNames)
		},
	}

	statusCmd.Flags().BoolVarP(&opts.Pretty, "pretty", "p", false, "pretty-print table")

	return statusCmd
}

// internalStatusModule is a default status module.
func internalStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var runningCtx running.RunningCtx
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	err := status.Status(runningCtx, opts)
	return err
}
