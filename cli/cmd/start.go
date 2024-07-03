package cmd

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/integrity"
)

var (
	// "watchdog" is a hidden flag used to daemonize a process.
	// In go, we can't just fork the process (reason - goroutines).
	// So, for daemonize, we restarts the process with "watchdog" flag.
	watchdog bool
	// integrityCheckPeriod is a flag enables periodic integrity checks.
	// The default period is 1 day.
	integrityCheckPeriod = 24 * 60 * 60
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
			util.HandleCmdErr(cmd, err)
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

	integrity.RegisterIntegrityCheckPeriodFlag(startCmd.Flags(), &integrityCheckPeriod)

	return startCmd
}

// startInstancesUnderWatchdog starts tarantool instances under tt watchdog.
func startInstancesUnderWatchdog(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	ttBin, err := os.Executable()
	if err != nil {
		return err
	}

	startArgs := []string{}
	if cmdCtx.Cli.IntegrityCheck != "" {
		startArgs = append(startArgs, "--integrity-check-period",
			strconv.Itoa(integrityCheckPeriod))
	}

	for _, instance := range instances {
		if err := running.StartWatchdog(cmdCtx, ttBin, instance, startArgs); err != nil {
			return err
		}
	}
	return nil
}

// internalStartModule is a default start module.
func internalStartModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if !isConfigExist(cmdCtx) {
		return errNoConfig
	}

	if cmdCtx.Cli.TarantoolCli.Executable == "" {
		return fmt.Errorf("cannot start: tarantool binary is not found")
	}

	var runningCtx running.RunningCtx
	if err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); err != nil {
		return err
	}

	if canStart, reason :=
		running.IsAbleToStartInstances(runningCtx.Instances, cmdCtx); !canStart {
		return fmt.Errorf(reason)
	}

	if !watchdog {
		if err := startInstancesUnderWatchdog(cmdCtx, runningCtx.Instances); err != nil {
			return err
		}
		return nil
	}

	checkPeriod := time.Duration(0)

	if cmdCtx.Cli.IntegrityCheck != "" && integrityCheckPeriod > 0 {
		checkPeriod = time.Duration(integrityCheckPeriod * int(time.Second))
	}

	if err := running.Start(cmdCtx, &runningCtx.Instances[0], checkPeriod); err != nil {
		return err
	}
	return nil
}
