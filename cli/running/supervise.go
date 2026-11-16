package running

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/cli/ttlog"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/pidfile"
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

// instanceSource is the Source of the watchdog of an instance: it starts the
// instance from its Launch, which tt instance start resolved, every time,
// and restarts it as the restart policy of the launch says. Nothing is read
// from the pair of the application meanwhile.
type instanceSource struct {
	cmdCtx *cmdcontext.CmdCtx
	// launch is what the watchdog runs; ran is the context of its instance,
	// whose sockets tarantool binds.
	launch *Launch
	ran    InstanceCtx
	log    *watchdogLog
	// stdout and stderr are where the output of tarantool goes; nil is the
	// log of the watchdog, tt.log.
	stdout io.Writer
	stderr io.Writer
}

func newInstanceSource(cmdCtx *cmdcontext.CmdCtx, launch *Launch, log *watchdogLog,
) *instanceSource {
	return &instanceSource{
		cmdCtx: cmdCtx,
		launch: launch,
		ran:    launch.Instance,
		log:    log,
		stdout: nil,
		stderr: nil,
	}
}

// Next returns the Spec of the launch, once what its start needs is there:
// the directories of the instance, the links of its version to the layout,
// when it runs on them, and the signature of what it runs, with the
// integrity checks on.
func (src *instanceSource) Next(context.Context) (supervisor.Spec, error) {
	inst := &src.ran

	err := prepareLaunch(inst, &src.launch.Spec)
	if err != nil {
		return supervisor.Spec{}, err
	}

	if inst.ClusterConfigPath != "" {
		src.log.logger.Printf("(INFO): using %q cluster config for instance %q",
			inst.ClusterConfigPath, inst.InstName)
	}

	return src.launch.Spec.supervisorSpec(outputOr(src.stdout, src.log.logger),
		outputOr(src.stderr, src.log.logger))
}

// prepareLaunch readies the start of inst from spec, its launch: the
// directories of the instance, which tt instance clean may have removed, the
// links of its version to the layout (linkIntegrityLayout), and, with the
// integrity checks on, the signature of the program it runs, which may have
// changed since the launch was resolved.
func prepareLaunch(inst *InstanceCtx, spec *LaunchSpec) error {
	err := createInstanceDataDirectories(*inst)
	if err != nil {
		return fmt.Errorf("failed to create a directory: %w", err)
	}

	err = linkIntegrityLayout(inst)
	if err != nil {
		return err
	}

	what := "tarantool"
	if IsExternal(inst) {
		what = "binary"
	}

	return checkSigned(inst, what, spec.Path)
}

// Restart removes the sockets tarantool left as it exited, then restarts the
// instance as the restart policy of its launch says (restartsAfter).
func (src *instanceSource) Restart(_ context.Context, exit supervisor.Exit) (bool, error) {
	removeSockets(&src.ran)

	return restartsAfter(&src.ran, exit), nil
}

// restartsAfter reports whether the watchdog starts inst again after exit, an
// exit of its own: an exit that follows tt instance stop or a failed
// integrity check never gets this far (supervisor.Source.Restart).
//
// The restart field of the instance record decides, with the meanings the
// instances package gives its values: no never restarts, always restarts
// after any exit, and on-failure after a non-zero exit status or death by a
// signal. An exit that could not be waited for counts as a failure, since
// nothing says the instance ended well.
func restartsAfter(inst *InstanceCtx, exit supervisor.Exit) bool {
	switch inst.Restart {
	case instances.RestartAlways:
		return true
	case instances.RestartOnFailure:
		return exit.State == nil || !exit.State.Success()
	default:
		return false
	}
}

// outputOr returns out, the writer the output of tarantool goes to, or log,
// the log of the watchdog, when out is nil.
func outputOr(out, log io.Writer) io.Writer {
	if out != nil {
		return out
	}

	return log
}

// started records the application directory the instance runs from in the
// run directory, for tt instance status and tt package to tell the version it
// runs (RunningAppDir).
func (src *instanceSource) started() {
	err := recordAppDir(&src.ran)
	if err != nil {
		src.log.logger.Printf("(WARN): %v.", err)
	}
}

// exited removes the pid file tarantool pid wrote where tt tells it to,
// TT_PROCESS_PID_FILE_DEFAULT, once it has exited, if the file still names
// it. Tarantool removes the file on a clean exit only: one that dies leaves
// it, and so does Tarantool 3.3 that a stop signal reaches during its first
// box.cfg, which ends with a fatal error. A pid file the config of the
// instance puts elsewhere is not looked at.
func (src *instanceSource) exited(pid int) {
	path := filepath.Join(src.ran.RunDir, layout.TarantoolPidFileName)

	_, err := pidfile.RemoveFor(path, pid, 0, nil)
	if err != nil {
		src.log.logger.Printf("(WARN): cannot remove the pid file %q: %v.", path, err)
	}
}

// watchdogOptions are the options of the watchdog of an instance: the pid file
// names the watchdog, the stop signals reach tarantool as they were sent, a
// SIGHUP rotates the log of the watchdog and reaches tarantool as well, once
// tarantool handles it, and
// the periodic integrity check, when there is one, is a hard stop. Once the
// watchdog holds the pid file, the launch it runs goes into the run
// directory (Launch.publish), for tt instance status --details, and is
// removed at the end with the sockets of the instance.
//
// The watchdog of an external process writes the pid of the process into its
// own pid file as well, since the process leads a group of its own that
// tt instance kill has to find (killProcess); a SIGHUP reaches the process as
// its logrotate_signal, if it has one (externalSpec).
func watchdogOptions(cmdCtx *cmdcontext.CmdCtx, src *instanceSource) supervisor.Options {
	opts := supervisor.Options{
		PidFile:       src.ran.PIDFile,
		ChildPidFile:  src.ran.ProcessPIDFile,
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
			src.launch.unpublish()

			err := removeAppDir(&src.ran)
			if err != nil {
				src.log.logger.Printf("(WARN): %v.", err)
			}
		},
		OnEvent: func(event supervisor.Event) {
			switch event := event.(type) {
			case supervisor.Started:
				src.started()
			case supervisor.PidFileWritten:
				if event.Path == src.ran.PIDFile {
					err := src.launch.publish(event.Pid)
					if err != nil {
						src.log.logger.Printf("(WARN): %v.", err)
					}
				}
			case supervisor.Exit:
				src.exited(event.Pid)
			}

			src.log.onEvent(event)
		},
		Start: nil,
	}

	if src.log.checkPeriod != 0 {
		// The checks of the instance: they know the files of its
		// application and its runtimes, wherever the watchdog runs.
		checks := src.ran.Integrity

		opts.CheckPeriod = src.log.checkPeriod
		opts.Check = func(context.Context) error {
			if checks == nil {
				return cmdCtx.Integrity.Repository.ValidateAll()
			}

			return checks.Repository.ValidateAll()
		}
	}

	return opts
}
