package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/v3/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/status"
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
	// watchdogReport is --report, the report file of the start that waits
	// for the instance of the watchdog (running.WaitUp).
	watchdogReport string
	// startWait is how tt instance start and restart wait for the instances
	// they start in the background.
	startWait waitOptions
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
		Short: "Start instances",
		Long: "Start the instances the targets select. By default every instance runs " +
			"in the background under a watchdog of tt, which restarts it as the restart " +
			"field of its record says.\n\n" + validateHelp + "\n\n" + waitHelp + "\n\n" +
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
	addWaitFlags(startCmd)
	addValidateFlag(startCmd)
	startCmd.Flags().BoolVar(&watchdog, running.WatchdogFlag, false, "")
	startCmd.Flags().StringVar(&watchdogLaunch, running.LaunchFlag, "", "")
	startCmd.Flags().StringVar(&watchdogReport, running.ReportFlag, "", "")

	for _, name := range []string{running.WatchdogFlag, running.LaunchFlag, running.ReportFlag} {
		_ = startCmd.Flags().MarkHidden(name)
	}

	integrityprovider.Get().RegisterIntegrityCheckPeriodFlag(startCmd.Flags(),
		&cmdCtx.Cli.IntegrityCheckPeriod)

	return startCmd
}

// waitHelp is what the help of tt instance start and restart says of the
// wait for the instances.
const waitHelp = "In the background tt waits until every instance it starts is up, " +
	"all of them at once, for --timeout at most: a Tarantool instance once its box " +
	"runs and its config is applied, as tt instance status shows them in BOX and " +
	"CONFIG, an external process once it has run for a second, TCM once its HTTP " +
	"address answers. An instance that " +
	"exits before that has failed, also when its restart policy has its watchdog " +
	"start it again, and so has one that is not up in time, which is left to its " +
	"watchdog: tt says what happened, quotes the end of its tt.log and exits with " +
	"an error. --no-wait starts the watchdogs and returns at once."

// waitOptions are the flags of the wait of tt instance start and restart for
// the instances they start in the background.
type waitOptions struct {
	// timeout is --timeout, how long to wait.
	timeout time.Duration
	// noWait is --no-wait, not to wait at all.
	noWait bool
}

// errNotPositiveTimeout is the error for a --timeout that is not positive.
var errNotPositiveTimeout = errors.New("--timeout must be positive; --no-wait does not wait")

// addWaitFlags declares --timeout and --no-wait on cmd.
func addWaitFlags(cmd *cobra.Command) {
	cmd.Flags().DurationVar(&startWait.timeout, "timeout", running.DefaultStartTimeout,
		"how long to wait for the instances started in the background to come up")
	cmd.Flags().BoolVar(&startWait.noWait, "no-wait", false,
		"do not wait for the instances started in the background to come up")
}

// noValidate is --no-validate of tt instance start and restart: start the
// instances without validating their cluster config first.
var noValidate bool

// addValidateFlag declares --no-validate on cmd.
func addValidateFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&noValidate, "no-validate", false,
		"start without validating the cluster config against the schema of Tarantool")
}

// validateHelp is what the help of tt instance start and restart says of the
// validation of the cluster config.
const validateHelp = "The cluster config of the instances is validated against the " +
	"schema of the Tarantool config first, as tt cluster show --validate and tt instance " +
	"check validate it: a config Tarantool would refuse, such as one with an unknown " +
	"option, starts nothing, and the error names every problem. --no-validate starts " +
	"the instances all the same."

// validateConfigs validates the cluster config of instances before they
// start (running.ValidateConfigs), unless --no-validate says not to.
func validateConfigs(instances []running.InstanceCtx) error {
	if noValidate {
		return nil
	}

	err := running.ValidateConfigs(instances)
	if err != nil {
		return fmt.Errorf("%w\nfix it, or pass --no-validate to start all the same", err)
	}

	return nil
}

// check refuses a timeout the wait cannot keep.
func (wait waitOptions) check() error {
	if !wait.noWait && wait.timeout <= 0 {
		return util.NewArgError(errNotPositiveTimeout.Error())
	}

	return nil
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
// result those that do not start. Unless startWait says not to, it then
// waits for all of them to come up at once (running.WaitUp), and records
// those that do not as well.
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

	wait := !startWait.noWait
	started := make([]*running.StartedWatchdog, 0, len(instances))

	for idx := range instances {
		watchdog, err := running.StartWatchdog(cmdCtx, ttBin, instances[idx],
			watchdogStartArgs(cmdCtx), wait)

		switch {
		case err != nil:
			result.fail(names[idx], err)
		case watchdog.Waits():
			started = append(started, watchdog)
		default:
			reportTCM(&watchdog.Instance)
		}
	}

	if !wait || len(started) == 0 {
		return
	}

	for idx, err := range running.WaitUp(started, startWait.timeout, status.Probe) {
		name := running.GetAppInstanceName(started[idx].Instance)
		if err != nil {
			result.fail(name, err)

			continue
		}

		log.Infof("The instance %s is up.", name)
		reportTCM(&started[idx].Instance)
	}
}

// reportTCM says how to log into inst, when it is TCM that has started or
// runs already: its URL, its user and where its password is, never the
// password itself.
func reportTCM(inst *running.InstanceCtx) {
	if !running.IsTCM(inst) {
		return
	}

	access := inst.TCM.Access
	if access.URL == "" {
		var err error

		access, err = running.DescribeTCM(inst)
		if err != nil {
			log.Warnf("%s", err)

			return
		}
	}

	log.Infof("TCM: %s", access.Line())
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
	err := startWait.check()
	if err != nil {
		return err
	}

	err = refuseManaged(instances, "start", startedByItsUnit)
	if err != nil {
		return err
	}

	canStart, err := running.IsAbleToStartInstances(instances)
	if !canStart {
		return err
	}

	err = validateConfigs(instances)
	if err != nil {
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
// target only names the instance in the list of processes. A failure before
// the watchdog runs goes to the start that waits for it, through --report,
// as well: the standard error of the watchdog is the null device.
func runWatchdog(cmdCtx *cmdcontext.CmdCtx, target []string) error {
	if watchdogLaunch == "" {
		return util.NewArgError(errWatchdogWithoutLaunch.Error())
	}

	if len(target) != 1 {
		return util.NewArgError("the watchdog runs one instance")
	}

	launch, err := running.LoadLaunch(cmdCtx, watchdogLaunch)
	if err != nil {
		running.ReportEnd(watchdogReport, err)

		return err
	}

	cmdCtx.Cli.IntegrityCheckPeriod = integrityCheckPeriodOf(&cmdCtx.Cli)

	if startForeground {
		return startForegroundWatchdog(cmdCtx, launch)
	}

	return running.Start(cmdCtx, launch, watchdogReport)
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
