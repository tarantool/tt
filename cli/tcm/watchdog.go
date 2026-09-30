package tcm

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"syscall"
	"time"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// signalAction is what the watchdog does with a signal it receives.
type signalAction int

const (
	// stopTCM stops TCM and ends the watchdog.
	stopTCM signalAction = iota + 1
	// ignoreSignal drops the signal: TCM does not see it.
	ignoreSignal
)

// signalPolicy is what the watchdog does with the signals it receives. A
// signal it does not name is forwarded to the process group of TCM.
var signalPolicy = map[syscall.Signal]signalAction{
	syscall.SIGINT:  stopTCM,
	syscall.SIGTERM: stopTCM,
	syscall.SIGHUP:  stopTCM,
	syscall.SIGQUIT: stopTCM,
}

// signalsFor returns the signals the policy handles with action, in a
// stable order.
func signalsFor(action signalAction) []syscall.Signal {
	var signals []syscall.Signal

	for _, sig := range slices.Sorted(maps.Keys(signalPolicy)) {
		if signalPolicy[sig] == action {
			signals = append(signals, sig)
		}
	}

	return signals
}

// WatchdogOpts configure RunWatchdog.
type WatchdogOpts struct {
	// Executable is the TCM binary. It runs without arguments, in the
	// working directory and the environment of the watchdog.
	Executable string
	// PidFile is the pid file of the watchdog, ChildPidFile the one of TCM.
	PidFile      string
	ChildPidFile string
	// RestartDelay is the pause between an exit of TCM and its next start.
	RestartDelay time.Duration
	// StopTimeout is how long TCM may take to stop after SIGTERM before its
	// process group is killed with SIGKILL.
	StopTimeout time.Duration
	// CheckPeriod is how often Check runs while TCM runs; 0 runs no checks.
	CheckPeriod time.Duration
	// Check is the periodic integrity check. A failure kills TCM and ends
	// the watchdog, which does not start TCM again.
	Check func(ctx context.Context) error
}

// tcmSource runs TCM again after every exit it did not ask for.
type tcmSource struct {
	opts *WatchdogOpts
}

// Next returns the Spec of TCM: it runs in a process group of its own, which
// is stopped with SIGTERM whatever stop signal the watchdog received.
func (src tcmSource) Next(context.Context) (supervisor.Spec, error) {
	return supervisor.Spec{
		Path:              src.opts.Executable,
		Args:              nil,
		Env:               nil,
		Dir:               "",
		Stdin:             nil,
		Stdout:            nil,
		Stderr:            nil,
		StopSignal:        syscall.SIGTERM,
		ForwardStopSignal: false,
		StopTimeout:       src.opts.StopTimeout,
		ProcessGroup:      true,
	}, nil
}

// Restart starts TCM again whenever it exits.
func (tcmSource) Restart(context.Context, supervisor.Exit) (bool, error) {
	return true, nil
}

func onEvent(opts *WatchdogOpts) func(event supervisor.Event) {
	// stopping is set once a stop signal has arrived: the exit that follows
	// is the stop, not a failure.
	stopping := false

	return func(event supervisor.Event) {
		switch event := event.(type) {
		case supervisor.PidFileWritten:
			if event.Path == opts.ChildPidFile {
				log.Infof("Process PID %d written to %s", event.Pid, event.Path)
			} else {
				log.Infof("Watchdog PID %d written to %s", event.Pid, event.Path)
			}
		case supervisor.Started:
			log.Infof("Process started successfully")

			if opts.CheckPeriod != 0 {
				log.Infof("Starting periodic integrity checks each %s", opts.CheckPeriod)
			}
		case supervisor.SignalReceived:
			log.Infof("Received signal: %v", event.Signal)

			if event.Action == supervisor.ActionStop && !stopping {
				stopping = true

				log.Infof("Stopping process...")
			}

			if event.Err != nil {
				log.Warnf("Failed to handle %v: %v", event.Signal, event.Err)
			}
		case supervisor.Exit:
			if stopping {
				log.Infof("Process stopped.")
			} else {
				logExit(event)
			}
		case supervisor.Killed:
			log.Warnf("Process %d killed: %s", event.Pid, event.Reason)
		case supervisor.Restarting:
			log.Infof("Waiting %s before restart...", event.Delay)
		case supervisor.Checked:
			if event.Err != nil {
				log.Errorf("Periodic integrity check failed: %v", event.Err)
			} else {
				log.Infof("Periodic integrity check passed")
			}
		}
	}
}

func logExit(exit supervisor.Exit) {
	var exitErr *exec.ExitError

	switch {
	case exit.Err == nil:
		log.Infof("Process completed successfully.")
	case errors.As(exit.Err, &exitErr):
		log.Warnf("Process exited with error: %v", exit.Err)
	default:
		log.Errorf("Process failed: %v", exit.Err)
	}
}

// RunWatchdog runs TCM and keeps it running until the watchdog receives
// SIGINT, SIGTERM, SIGHUP or SIGQUIT, which stop TCM with SIGTERM, or a
// periodic check fails. It refuses to run while its own pid file names a
// running process, and kills the TCM it started and returns when the pid
// file of TCM does. Both pid files are removed before it returns.
func RunWatchdog(opts WatchdogOpts) error {
	engineOpts := supervisor.Options{
		PidFile:       opts.PidFile,
		ChildPidFile:  opts.ChildPidFile,
		StopSignals:   signalsFor(stopTCM),
		IgnoreSignals: signalsFor(ignoreSignal),
		ReloadSignal:  0,
		OnReload:      nil,
		RestartDelay:  opts.RestartDelay,
		CheckPeriod:   0,
		Check:         nil,
		Cleanup:       nil,
		OnEvent:       onEvent(&opts),
		Start:         nil,
	}

	if opts.CheckPeriod != 0 {
		engineOpts.CheckPeriod = opts.CheckPeriod
		engineOpts.Check = opts.Check
	}

	engine, err := supervisor.New(tcmSource{opts: &opts}, engineOpts)
	if err != nil {
		return fmt.Errorf("failed to set up the watchdog: %w", err)
	}

	err = engine.Run(context.Background())
	if err != nil {
		return fmt.Errorf("watchdog: %w", err)
	}

	log.Infof("Watchdog stopped.")

	return nil
}
