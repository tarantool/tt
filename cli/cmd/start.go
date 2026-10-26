package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
)

// errPinWithoutWatchdog reports a pin of the application directory given to
// tt instance start without --watchdog.
var errPinWithoutWatchdog = errors.New("--" + running.AppDirFlag +
	" is only for the watchdog of an instance")

var (
	// "watchdog" is a hidden flag used to daemonize a process.
	// In go, we can't just fork the process (reason - goroutines).
	// So, for daemonize, we restarts the process with "watchdog" flag.
	watchdog bool
	// watchdogDir is --app-dir, the application directory the watchdog runs
	// its instance from (StartWatchdog). The scope and the template come with
	// it as --scope and --template.
	watchdogDir string
	// integrityCheckPeriod is a flag that enables periodic integrity checks.
	// The default period is 1 day.
	integrityCheckPeriod = 24 * 60 * 60
	// startForeground is --foreground: tt stays in the foreground while the
	// watchdogs of the instances, which do not restart them, run under it;
	// with --watchdog it runs one of those watchdogs.
	startForeground bool
)

// NewStartCmd creates the start command of tt instance.
func NewStartCmd() *cobra.Command {
	startCmd := &cobra.Command{
		Use:   "start " + instanceTargetsUsage,
		Short: "Start tarantool instance(s)",
		Long: "Start the instances the targets select. By default every instance runs " +
			"in the background under a watchdog of tt, which restarts it as the restart " +
			"field of its record says.\n\n" +
			"With --" + running.ForegroundFlag + " tt stays in the foreground, for " +
			"systemd, a container or a terminal, while every instance runs under a " +
			"watchdog of its own that does not restart it. The output of the instances " +
			"goes to their tt.log and to the standard output, prefixed with the " +
			"instance when there are several. SIGINT, SIGTERM and SIGQUIT stop every " +
			"instance, as a stop of its watchdog does; tt instance stop stops one of " +
			"them from another terminal. An instance that ends does not stop the " +
			"others. tt exits once all of them have ended: with 0 when they were " +
			"stopped in time, with the exit code of the first instance that failed " +
			"otherwise, " + strconv.Itoa(running.ExitKilledOnStop) + " for one killed " +
			"as it did not stop in time.",
		RunE: internalRunE(internalStartModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Inactive)
		},
	}

	addInstanceFlags(startCmd)

	startCmd.Flags().BoolVar(&startForeground, running.ForegroundFlag, false,
		"stay in the foreground while the instances run, without restarting them")
	startCmd.Flags().BoolVar(&watchdog, running.WatchdogFlag, false, "")
	startCmd.Flags().StringVar(&watchdogDir, running.AppDirFlag, "", "")

	for _, name := range []string{running.WatchdogFlag, running.AppDirFlag} {
		_ = startCmd.Flags().MarkHidden(name)
	}

	integrity.RegisterIntegrityCheckPeriodFlag(startCmd.Flags(), &cmdCtx.Cli.IntegrityCheckPeriod)

	return startCmd
}

// startInstancesUnderWatchdog starts tarantool instances under tt watchdog.
func startInstancesUnderWatchdog(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	ttBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get the tt executable path: %w", err)
	}

	for _, instance := range instances {
		err := running.StartWatchdog(cmdCtx, ttBin, instance, watchdogStartArgs(cmdCtx))
		if err != nil {
			return err
		}
	}

	return nil
}

// watchdogStartArgs are the arguments of tt that go before those that run
// the watchdog of an instance: the period of the integrity checks, when
// they are on.
func watchdogStartArgs(cmdCtx *cmdcontext.CmdCtx) []string {
	if cmdCtx.Cli.IntegrityCheck == "" {
		return nil
	}

	return []string{
		"--integrity-check-period",
		//nolint:gosec // G115: the period is passed on exactly as tt has always formatted it.
		strconv.FormatUint(uint64(cmdCtx.Cli.IntegrityCheckPeriod), 10),
	}
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

	return startResolved(cmdCtx, instances)
}

// startResolved starts instances, resolved by tt instance start or anew by
// tt instance restart, as the flags of tt instance start say: in the
// background, in the foreground, or as the watchdog of one instance, of the
// background or of the foreground.
func startResolved(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	if watchdog && len(instances) != 1 {
		return util.NewArgError("the watchdog runs one instance")
	}

	// The watchdog of the foreground has been checked by tt in the
	// foreground, whose output its own would repeat.
	if !watchdog || !startForeground {
		canStart, err := running.IsAbleToStartInstances(instances)
		if !canStart {
			return err
		}
	}

	switch {
	case !watchdog && startForeground:
		return startInstancesForeground(cmdCtx, instances)
	case !watchdog:
		return startInstancesUnderWatchdog(cmdCtx, instances)
	}

	cmdCtx.Cli.IntegrityCheckPeriod = integrityCheckPeriodOf(&cmdCtx.Cli)

	if startForeground {
		return startForegroundWatchdog(cmdCtx, &instances[0])
	}

	return running.Start(context.Background(), cmdCtx, &instances[0])
}

// startOptions are the options tt instance start resolves its targets with.
// The watchdog of an instance gets the scope, the template and the
// application directory the instance was resolved with, so that it runs it
// from the application directory the command that started it found
// (running.StartWatchdog).
func startOptions(cmdCtx *cmdcontext.CmdCtx) (app.Options, error) {
	if watchdogDir != "" && !watchdog {
		return app.Options{}, util.NewArgError(errPinWithoutWatchdog.Error())
	}

	opts, err := instanceTargetOptions(cmdCtx)
	if err != nil {
		return app.Options{}, err
	}

	opts.Dir = watchdogDir

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
