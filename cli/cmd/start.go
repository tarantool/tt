package cmd

import (
	"os"
	"os/exec"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
)

var (
	// "watchdog" is a hidden flag used to daemonize a process.
	// In go, we can't just fork the process (reason - goroutines).
	// So, for daemonize, we restarts the process with "watchdog" flag.
	watchdog bool
)

// NewStartCmd creates start command.
func NewStartCmd() *cobra.Command {
	var startCmd = &cobra.Command{
		Use:   "start [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Start tarantool instance(s)",
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalStartModule, args)
			handleCmdErr(cmd, err)
		},
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractInactiveAppNames,
				running.ExtractInactiveInstanceNames)
		},
	}

	startCmd.Flags().BoolVar(&watchdog, "watchdog", false, "")
	startCmd.Flags().MarkHidden("watchdog")

	return startCmd
}

// internalStartModule is a default start module.
func internalStartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	var runningCtx running.RunningCtx
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	if !watchdog {
		ttBin, err := os.Executable()
		if err != nil {
			return err
		}
		for _, run := range runningCtx.Instances {
			appName := running.GetAppInstanceName(run)
			// If an instance is already running don't try to start it again.
			// For restarting an instance use tt restart command.
			procStatus := process_utils.ProcessStatus(run.PIDFile)
			if procStatus.Code ==
				process_utils.ProcStateRunning.Code {
				log.Infof("The instance %s (PID = %d) is already running.",
					appName, procStatus.PID)
				continue
			}

			log.Infof("Starting an instance [%s]...", appName)

			newArgs := []string{"start", "--watchdog", appName}

			wdCmd := exec.Command(ttBin, newArgs...)

			if err := wdCmd.Start(); err != nil {
				return err
			}
		}

		return nil
	}

	if err := running.Start(cmdCtx, &runningCtx.Instances[0]); err != nil {
		return err
	}
	return nil
}
