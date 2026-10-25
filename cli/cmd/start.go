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
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/tail"
	"github.com/tarantool/tt/v3/cli/util"
)

// errPinWithoutWatchdog reports a pin of the application directory given to
// tt start without --watchdog.
var errPinWithoutWatchdog = errors.New("--" + running.AppDirFlag +
	" is only for the watchdog of an instance")

var (
	// "watchdog" is a hidden flag used to daemonize a process.
	// In go, we can't just fork the process (reason - goroutines).
	// So, for daemonize, we restarts the process with "watchdog" flag.
	watchdog bool
	// watchdogPin is what the hidden flags that come with --watchdog say of
	// the application the watchdog runs its instance from (StartWatchdog).
	watchdogPin struct {
		scope    string
		template string
		dir      string
	}
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
		Use:   "start " + instanceTargetsUsage,
		Short: "Start tarantool instance(s)",
		RunE:  internalRunE(internalStartModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(
				&cmdCtx, cmd, toComplete,
				running.ExtractInactiveAppNames,
				running.ExtractInactiveInstanceNames)
		},
	}

	startCmd.Flags().BoolVar(&watchdog, running.WatchdogFlag, false, "")
	startCmd.Flags().StringVar(&watchdogPin.scope, running.ScopeFlag, "", "")
	startCmd.Flags().StringVar(&watchdogPin.template, running.TemplateFlag, "", "")
	startCmd.Flags().StringVar(&watchdogPin.dir, running.AppDirFlag, "", "")

	for _, name := range []string{
		running.WatchdogFlag, running.ScopeFlag, running.TemplateFlag, running.AppDirFlag,
	} {
		_ = startCmd.Flags().MarkHidden(name)
	}

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
	opts, err := startOptions(cmdCtx)
	if err != nil {
		return err
	}

	instances, err := resolveInstancesWith(args, opts)
	if err != nil {
		return err
	}

	if canStart, err := running.IsAbleToStartInstances(instances); !canStart {
		return err
	}

	if !watchdog {
		return startInstances(cmdCtx, instances)
	}

	if len(instances) != 1 {
		return util.NewArgError("the watchdog runs one instance")
	}

	cmdCtx.Cli.IntegrityCheckPeriod = integrityCheckPeriodOf(&cmdCtx.Cli)

	return running.Start(context.Background(), cmdCtx, &instances[0])
}

// startOptions are the options tt start resolves its targets with. The
// watchdog of an instance gets the scope, the template and the application
// directory the instance was resolved with, so that it runs it from the
// application directory the command that started it found
// (running.StartWatchdog).
func startOptions(cmdCtx *cmdcontext.CmdCtx) (app.Options, error) {
	opts, err := instanceOptions(cmdCtx)
	if err != nil {
		return app.Options{}, err
	}

	if watchdogPin.dir != "" && !watchdog {
		return app.Options{}, util.NewArgError(errPinWithoutWatchdog.Error())
	}

	opts.Scope, err = app.ParseScope(watchdogPin.scope)
	if err != nil {
		return app.Options{}, err
	}

	opts.Template = watchdogPin.template
	opts.Dir = watchdogPin.dir

	return opts, nil
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
