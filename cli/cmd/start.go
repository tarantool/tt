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
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/tail"
)

var (
	errTarantoolBinaryNotFound = errors.New("tarantool binary is not found")
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
	startCmd := &cobra.Command{
		Use:   "start [<APP_NAME> | <APP_NAME:INSTANCE_NAME>]",
		Short: "Start tarantool instance(s)",
		RunE:  internalRunE(internalStartModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractInactiveAppNames,
				running.ExtractInactiveInstanceNames)
		},
	}

	startCmd.Flags().BoolVar(&watchdog, "watchdog", false, "")

	_ = startCmd.Flags().MarkHidden("watchdog")
	startCmd.Flags().BoolVarP(&startInteractive, "interactive", "i", false, "")

	integrity.RegisterIntegrityCheckPeriodFlag(startCmd.Flags(), &cmdCtx.Cli.IntegrityCheckPeriod)

	return startCmd
}

// startInstancesUnderWatchdog starts tarantool instances under tt watchdog.
func startInstancesUnderWatchdog(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	ttBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get the tt executable path: %w", err)
	}

	startArgs := []string{}
	if cmdCtx.Cli.IntegrityCheck != "" {
		startArgs = append(startArgs, "--integrity-check-period",
			//nolint:gosec // G115: the period is passed on exactly as tt has always formatted it.
			strconv.FormatUint(uint64(cmdCtx.Cli.IntegrityCheckPeriod), 10))
	}

	for _, instance := range instances {
		err := running.StartWatchdog(cmdCtx, ttBin, instance, startArgs)
		if err != nil {
			return err
		}
	}

	return nil
}

// startInstancesInteractive starts tarantool instances and waits for them to complete.
func startInstancesInteractive(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	waitGroup := sync.WaitGroup{}
	pickColor := tail.DefaultColorPicker()

	for _, instCtx := range instances {
		clr := pickColor()
		prefix := running.GetAppInstanceName(instCtx) + " "

		waitGroup.Add(1)

		go func(inst running.InstanceCtx) {
			_ = running.RunInstance(ctx, cmdCtx, inst,
				running.NewColorizedPrefixWriter(os.Stdout, clr, prefix),
				running.NewColorizedPrefixWriter(os.Stderr, clr, prefix))

			waitGroup.Done()
		}(instCtx)
	}

	waitGroup.Wait()

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
		return fmt.Errorf("cannot start: %w", errTarantoolBinaryNotFound)
	}

	var runningCtx running.RunningCtx

	err := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args, running.ConfigLoadAll)
	if err != nil {
		return err
	}

	if canStart, err := running.IsAbleToStartInstances(runningCtx.Instances, cmdCtx); !canStart {
		return err
	}

	if !watchdog {
		return startInstances(cmdCtx, runningCtx.Instances)
	}

	cmdCtx.Cli.IntegrityCheckPeriod = integrityCheckPeriodOf(&cmdCtx.Cli)

	return running.Start(cmdCtx, &runningCtx.Instances[0])
}

// integrityCheckPeriodOf returns the integrity check period of a watchdog in
// seconds: --integrity-check-period, or the default one when integrity
// checking is on without a period.
func integrityCheckPeriodOf(cli *cmdcontext.CliCtx) int {
	if cli.IntegrityCheck != "" && cli.IntegrityCheckPeriod == 0 {
		return integrityCheckPeriod
	}

	return cli.IntegrityCheckPeriod
}
