package running

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/configure"
	"github.com/tarantool/tt/v3/cli/ttlog"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// watchdogLog is the log of the watchdog of an instance, which the log
// settings may replace between two starts of the instance.
type watchdogLog struct {
	logger ttlog.Logger
	// checkPeriod is the period of the integrity checks, 0 without them.
	checkPeriod time.Duration
}

func (wl *watchdogLog) onEvent(event supervisor.Event) {
	switch event := event.(type) {
	case supervisor.Started:
		if wl.checkPeriod != 0 {
			wl.logger.Printf("(INFO): starting periodic integrity checks each %s.",
				wl.checkPeriod)
		}
	case supervisor.SignalReceived:
		wl.logger.Printf("(INFO): %s received.", event.Signal)

		if event.Err != nil {
			wl.logger.Printf("(WARN): %s: %v.", event.Signal, event.Err)
		}
	case supervisor.Exit:
		if event.Err != nil {
			wl.logger.Printf(`(WARN): "%v".`, event.Err)
		}
	case supervisor.Killed:
		wl.logger.Printf("(WARN): the instance was killed: %s.", event.Reason)
	case supervisor.Restarting:
		wl.logger.Printf(`(INFO): waiting for restart timeout %s.`, event.Delay)
	case supervisor.Checked:
		if event.Err != nil {
			wl.logger.Printf("(ERROR): periodic integrity check failed: %q.", event.Err)
		} else {
			wl.logger.Printf("(INFO): periodic integrity check successfully passed.")
		}
	case supervisor.PidFileWritten:
	}
}

// onEnd writes the last log line of the watchdog, for the error Run ended
// with.
func (wl *watchdogLog) onEnd(err error) {
	var runErr *supervisor.Error

	switch {
	case err == nil:
		wl.logger.Println("(INFO): the Instance has shutdown.")
	case !errors.As(err, &runErr):
		wl.logger.Printf("(ERROR): %v.", err)
	case runErr.Op == supervisor.OpPidFile:
		wl.logger.Printf(`(ERROR): Pre-start action error: %v`, runErr.Err)
	case runErr.Op == supervisor.OpNext:
		wl.logger.Printf(`(ERROR): instance creation failed: %v.`, runErr.Err)
	case runErr.Op == supervisor.OpStart:
		wl.logger.Printf(`(ERROR): instance start failed: %v.`, runErr.Err)
	case runErr.Op == supervisor.OpCheck:
		wl.logger.Printf("(ERROR): the instance is stopped by a failed integrity check "+
			"and is not restarted: %v.", runErr.Err)
	default:
		wl.logger.Printf("(ERROR): %v.", err)
	}
}

// instanceSource is the Source of the watchdog of an instance. It reads the
// configuration again before every start and after every exit, so that a
// change of the configuration applies from the next start on.
type instanceSource struct {
	cmdCtx *cmdcontext.CmdCtx
	// ran is the context tarantool started last runs, or ran, with: the one
	// whose sockets are on disk. Before the first start it is the context
	// the watchdog was started for.
	ran InstanceCtx
	// next is the context the last Spec was built from. It becomes ran once
	// tarantool has started with it.
	next InstanceCtx
	log  *watchdogLog
	// refresh reads the context of the instance from the configuration.
	refresh func() (InstanceCtx, error)
}

func newInstanceSource(cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx, log *watchdogLog,
	refresh func() (InstanceCtx, error),
) *instanceSource {
	return &instanceSource{cmdCtx: cmdCtx, ran: *inst, next: *inst, log: log, refresh: refresh}
}

// Next reads the configuration and builds the Spec of the instance from it.
func (src *instanceSource) Next(context.Context) (supervisor.Spec, error) {
	inst, err := src.refresh()
	if err != nil {
		return supervisor.Spec{}, err
	}

	src.next = inst

	if inst.ClusterConfigPath != "" {
		src.log.logger.Printf("(INFO): using %q cluster config for instance %q",
			inst.ClusterConfigPath, inst.InstName)
	}

	return instanceSpec(src.cmdCtx, &src.next, specOptions{
		integrity: integrityOf(src.cmdCtx),
		stdout:    src.log.logger,
		stderr:    src.log.logger,
	})
}

// Restart removes the sockets tarantool left as it exited, then restarts the
// instance as its current restart_on_failure setting says, and switches to a
// new log if the log settings have changed.
func (src *instanceSource) Restart(context.Context, supervisor.Exit) (bool, error) {
	removeSockets(&src.ran)

	inst, err := src.refresh()
	if err != nil {
		src.log.logger.Println("(ERROR): can't check if the instance is restartable.")

		return false, nil //nolint:nilerr // Logged above; the watchdog ends.
	}

	if !inst.Restartable {
		return false, nil
	}

	logger, err := updateLogger(src.log.logger, &inst)
	if err != nil {
		src.log.logger.Println("(ERROR): can't update logger parameters.")

		return false, nil //nolint:nilerr // Logged above; the watchdog ends.
	}

	src.log.logger = logger

	return true, nil
}

// started records that tarantool runs with the context of the last Spec.
func (src *instanceSource) started() {
	src.ran = src.next
}

func refreshFromConfig(cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx) (InstanceCtx, error) {
	cliOpts, _, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath, cmdCtx.Integrity.Repository)
	if err != nil {
		return InstanceCtx{}, err
	}

	var args []string

	if inst.SingleApp {
		args = []string{inst.AppName}
	} else {
		args = []string{inst.AppName + string(InstanceDelimiter) + inst.InstName}
	}

	var runningCtx RunningCtx

	err = FillCtx(cliOpts, cmdCtx, &runningCtx, args, ConfigLoadSkip)
	if err != nil {
		return InstanceCtx{}, err
	}

	return runningCtx.Instances[0], nil
}

// isLoggerChanged reports whether instanceCtx names another log file than
// the one logger writes; no logger at all counts as a change.
func isLoggerChanged(logger ttlog.Logger, instanceCtx *InstanceCtx) (bool, error) {
	if logger == nil {
		return true, nil
	}

	if instanceCtx == nil {
		return true, errLoggerChangedCheckFailedPassingNullAsAnInstanceContext
	}

	loggerOpts := logger.GetOpts()

	if loggerOpts.Filename != instanceCtx.Log {
		return true, nil
	}

	return false, nil
}

// updateLogger returns logger, or a new one for the log settings of
// instanceCtx if they have changed.
func updateLogger(logger ttlog.Logger, instanceCtx *InstanceCtx) (ttlog.Logger, error) {
	changed, err := isLoggerChanged(logger, instanceCtx)
	if err != nil {
		return logger, err
	}

	if !changed {
		return logger, nil
	}

	if logger != nil {
		_ = logger.Close()
	}

	return createLogger(instanceCtx)
}

// watchdogOptions are the options of the watchdog of an instance: the pid file
// names the watchdog, the stop signals reach tarantool as they were sent, a
// SIGHUP rotates the log of the watchdog and reaches tarantool as well, and
// the periodic integrity check, when there is one, is a hard stop. The
// sockets removed at the end are those of the context tarantool last ran
// with, whatever the configuration says by then.
func watchdogOptions(cmdCtx *cmdcontext.CmdCtx, src *instanceSource) supervisor.Options {
	opts := supervisor.Options{
		PidFile:       src.ran.PIDFile,
		ChildPidFile:  "",
		StopSignals:   []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT},
		IgnoreSignals: nil,
		ReloadSignal:  syscall.SIGHUP,
		OnReload: func() error {
			return src.log.logger.Rotate()
		},
		RestartDelay: watchdogRestartTimeout,
		CheckPeriod:  0,
		Check:        nil,
		Cleanup: func() {
			removeSockets(&src.ran)
		},
		OnEvent: func(event supervisor.Event) {
			_, started := event.(supervisor.Started)
			if started {
				src.started()
			}

			src.log.onEvent(event)
		},
		Start: nil,
	}

	if src.log.checkPeriod != 0 {
		opts.CheckPeriod = src.log.checkPeriod
		opts.Check = func(context.Context) error {
			return cmdCtx.Integrity.Repository.ValidateAll()
		}
	}

	return opts
}
