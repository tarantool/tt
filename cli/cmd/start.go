package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/tail"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/lib/integrity"
)

var (
	// "watchdog" is a hidden flag used to daemonize a process.
	// In go, we can't just fork the process (reason - goroutines).
	// So, for daemonize, we restarts the process with "watchdog" flag.
	watchdog bool
	// integrityCheckPeriod is a flag that enables periodic integrity checks.
	// The default period is 1 day.
	integrityCheckPeriod = 24 * 60 * 60
	// startInteractive is startInteractive mode flag. If set, the main process does not exit after
	// watchdog children start and waits for them to complete. Also all logging is performed
	// to standard output.
	startInteractive bool
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
	startCmd.Flags().BoolVarP(&startInteractive, "interactive", "i", false, "")

	integrity.RegisterIntegrityCheckPeriodFlag(startCmd.Flags(), &cmdCtx.Cli.IntegrityCheckPeriod)

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
			strconv.FormatUint(uint64(cmdCtx.Cli.IntegrityCheckPeriod), 10))
	}

	for _, instance := range instances {
		if err := running.StartWatchdog(cmdCtx, ttBin, instance, startArgs); err != nil {
			return err
		}
	}
	return nil
}

// startInstancesInteractive starts tarantool instances and waits for them to complete.
func startInstancesInteractive(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	wg := sync.WaitGroup{}
	pickColor := tail.DefaultColorPicker()
	for _, instCtx := range instances {
		clr := pickColor()
		prefix := running.GetAppInstanceName(instCtx) + " "
		wg.Add(1)
		go func(inst running.InstanceCtx) {
			running.RunInstance(ctx, cmdCtx, inst,
				running.NewColorizedPrefixWriter(os.Stdout, clr, prefix),
				running.NewColorizedPrefixWriter(os.Stderr, clr, prefix))
			wg.Done()
		}(instCtx)
	}
	wg.Wait()
	return nil
}

// startInstances starts tarantool instances.
func startInstances(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	if startInteractive {
		return startInstancesInteractive(cmdCtx, instances)
	}
	return startInstancesUnderWatchdog(cmdCtx, instances)
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
		return errors.New(reason)
	}

	if !watchdog {
		if err := startInstances(cmdCtx, runningCtx.Instances); err != nil {
			return err
		}
		return nil
	}

	if cmdCtx.Cli.IntegrityCheck != "" && cmdCtx.Cli.IntegrityCheckPeriod == 0 {
		cmdCtx.Cli.IntegrityCheckPeriod = integrityCheckPeriod
	}

	if err := running.Start(cmdCtx, &runningCtx.Instances[0]); err != nil {
		return err
	}
	return nil
}
