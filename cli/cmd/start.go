package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

var (
	// errLaunchWithoutWatchdog reports a launch given to tt instance start
	// without --watchdog.
	errLaunchWithoutWatchdog = errors.New("--" + running.LaunchFlag +
		" is only for the watchdog of an instance")
	// errWatchdogWithoutLaunch reports a watchdog started without the launch
	// it runs.
	errWatchdogWithoutLaunch = errors.New("the watchdog of an instance needs --" +
		running.LaunchFlag)
)

var (
	// "watchdog" is a hidden flag used to daemonize a process.
	// In go, we can't just fork the process (reason - goroutines).
	// So, for daemonize, we restarts the process with "watchdog" flag.
	watchdog bool
	// watchdogLaunch is --launch, the file of the launch the watchdog runs
	// its instance from (running.StartWatchdog), which tt instance start
	// resolved.
	watchdogLaunch string
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
		Short: "Start instance(s)",
		Long: "Start the instances the targets select. By default every instance runs " +
			"in the background under a watchdog of tt, which restarts it as the restart " +
			"field of its record says.\n\n" +
			"What an instance runs - its binary or tarantool, its script or cluster " +
			"config, its arguments, environment, restart policy and paths - is resolved " +
			"here, before its watchdog starts, and the watchdog runs that for its whole " +
			"life, restarts included: an edit of instances.yaml takes effect at the next " +
			"tt instance start or restart. tarantool reads config.yaml itself at every " +
			"start.\n\n" +
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
	startCmd.Flags().StringVar(&watchdogLaunch, running.LaunchFlag, "", "")

	for _, name := range []string{running.WatchdogFlag, running.LaunchFlag} {
		_ = startCmd.Flags().MarkHidden(name)
	}

	integrityprovider.Get().RegisterIntegrityCheckPeriodFlag(startCmd.Flags(),
		&cmdCtx.Cli.IntegrityCheckPeriod)

	return startCmd
}

// startInstancesUnderWatchdog starts instances under tt watchdog.
// An instance whose watchdog does not start does not keep the others from
// starting (instancesResult).
func startInstancesUnderWatchdog(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	result := newInstancesResult("start", len(instances))

	startWatchdogs(cmdCtx, instances, result)

	return result.err()
}

// startWatchdogs starts the watchdogs of instances, in order, and records in
// result those that do not start.
func startWatchdogs(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx,
	result *instancesResult,
) {
	names := make([]string, 0, len(instances))
	for i := range instances {
		names = append(names, running.GetAppInstanceName(instances[i]))
	}

	ttBin, err := os.Executable()
	if err != nil {
		result.failAll(names, fmt.Errorf("failed to get the tt executable path: %w", err))

		return
	}

	for i := range instances {
		err := running.StartWatchdog(cmdCtx, ttBin, instances[i], watchdogStartArgs(cmdCtx))
		if err != nil {
			result.fail(names[i], err)
		}
	}
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
	switch {
	case watchdog:
		return runWatchdog(cmdCtx, args)
	case watchdogLaunch != "":
		return util.NewArgError(errLaunchWithoutWatchdog.Error())
	}

	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	return startResolved(cmdCtx, instances)
}

// startResolved starts instances, resolved by tt instance start or anew by
// tt instance restart, as the flags of tt instance start say: in the
// background or in the foreground.
//
// An instance that a systemd unit manages is refused, but when its unit
// starts it (startedByItsUnit).
func startResolved(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	err := refuseManaged(instances, "start", startedByItsUnit)
	if err != nil {
		return err
	}

	canStart, err := running.IsAbleToStartInstances(instances)
	if !canStart {
		return err
	}

	if startForeground {
		return startInstancesForeground(cmdCtx, instances)
	}

	return startInstancesUnderWatchdog(cmdCtx, instances)
}

// runWatchdog runs the watchdog of one instance, of the background or of the
// foreground, from the launch --launch names, which the tt that started it
// resolved and checked (running.StartWatchdog). It resolves nothing itself:
// target only names the instance in the list of processes.
func runWatchdog(cmdCtx *cmdcontext.CmdCtx, target []string) error {
	if watchdogLaunch == "" {
		return util.NewArgError(errWatchdogWithoutLaunch.Error())
	}

	if len(target) != 1 {
		return util.NewArgError("the watchdog runs one instance")
	}

	launch, err := running.LoadLaunch(cmdCtx, watchdogLaunch)
	if err != nil {
		return err
	}

	cmdCtx.Cli.IntegrityCheckPeriod = integrityCheckPeriodOf(&cmdCtx.Cli)

	if startForeground {
		return startForegroundWatchdog(cmdCtx, launch)
	}

	return running.Start(cmdCtx, launch)
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
