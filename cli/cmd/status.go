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
		Long: `The 'status' command provides information about the status of Tarantool instances.

Columns:
- INSTANCE: The name of the Tarantool instance.
- STATUS: The current status of the instance:
	- RUNNING: The instance is up and running.
	- NOT RUNNING: The instance is not running.
	- ERROR: The process has terminated unexpectedly.
- PID: The watchdog process PID.
- MODE: The mode of the instance, indicating its read/write status:
	- RO: The instance is in read-only mode.
	- RW: The instance is in read-write mode.
- CONFIG: The config info status (for Tarantool 3+).
- BOX: The box info status.
- UPSTREAM: The replication upstream status.`,
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
	statusCmd.Flags().BoolVarP(&opts.Details, "details", "d", false, "print detailed alerts.")

	return statusCmd
}

// internalStatusModule is a default status module.
func internalStatusModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var runningCtx running.RunningCtx
	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, running.ConfigLoadSkip)
	if err != nil {
		return err
	}

	err = status.Status(runningCtx, opts)
	return err
}
