package running

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// tt instance start --foreground runs every one of its instances under a
// watchdog of its own (decision 37): the watchdog of the background, as
// tt instance start --watchdog --foreground runs it (StartForeground), in a
// child process of the tt in the foreground (StartForegroundWatchdog). The
// watchdog leads a process group of its own and keeps the pid file of its
// instance, so tt instance stop, kill, logrotate and status reach one
// instance from another terminal as they reach one started in the
// background, and a signal the terminal sends to the tt in the foreground,
// such as the SIGINT of Ctrl-C, reaches the instances only as that tt passes
// it on, once. Unlike the watchdog of the background it never restarts its
// instance, whatever the restart policy says: the restart is up to whoever
// runs tt, such as systemd. It writes the output of the instance into
// tt.log, as in the background, and copies it to its standard output, which
// the tt in the foreground reads.
//
// The exit code of that watchdog tells the tt in the foreground how its
// instance ended:
//
//   - 0: the instance was stopped by a stop signal the watchdog received -
//     from the tt in the foreground, or tt instance stop - and exited within
//     its stop timeout, whatever its exit status: a requested stop is no
//     failure, and does not come out as 143, the code of a SIGTERM; or the
//     instance exited on its own with 0; or the stop came before it started;
//   - ExitKilledOnStop: the instance did not exit within its stop timeout
//     after a stop and was killed;
//   - the exit status of the instance, or 128 plus the number of the signal
//     that killed it, when it ended on its own;
//   - 1: the watchdog failed itself - the instance could not start, its pid
//     file was taken, a periodic integrity check failed - and logged why on
//     its standard error.
//
// An instance that exits on its own with 1, or is killed by SIGKILL on its
// own, comes out as the failure of the watchdog or as a kill on a stop: all
// three are failures, and the log tells which.

// ForegroundFlag is the flag of tt instance start that runs the instances in
// the foreground, and with WatchdogFlag runs the watchdog of one of them.
const ForegroundFlag = "foreground"

const (
	// signalExitBase is what the exit code of a process killed by a signal
	// is counted from, as the shell counts it: 128 plus the number of the
	// signal.
	signalExitBase = 128
	// ExitKilledOnStop is the exit code of the watchdog of tt instance start
	// --foreground whose instance did not stop in time and was killed: that
	// of a process killed by SIGKILL.
	ExitKilledOnStop = signalExitBase + int(syscall.SIGKILL)
	// watchdogFailure is the exit code of the watchdog of tt instance start
	// --foreground that failed itself.
	watchdogFailure = 1
	// echoBuffer is how many writes of the output of an instance wait for
	// the standard output of its watchdog before new ones are dropped.
	echoBuffer = 1024
	// echoDrainTimeout bounds the wait for the output of an instance to reach
	// the standard output when its watchdog ends.
	echoDrainTimeout = 5 * time.Second
)

// ExitCodeOf returns the exit code of the process that ended as state says:
// its exit status, or 128 plus the number of the signal that killed it.
func ExitCodeOf(state *os.ProcessState) int {
	status, ok := state.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		return signalExitBase + int(status.Signal())
	}

	return state.ExitCode()
}

// StartForeground runs the watchdog of inst as tt instance start --foreground
// runs it, in the current process: the watchdog Start runs, which never
// restarts the instance and copies its output to echo as well. It returns
// the exit code that tells how the instance ended, as the comment above
// says, or the error that kept the watchdog from running.
func StartForeground(ctx context.Context, cmdCtx *cmdcontext.CmdCtx, inst *InstanceCtx,
	echo io.Writer,
) (int, error) {
	pinned := *inst

	output := newAsyncEcho(echo)
	defer output.close(echoDrainTimeout)

	//nolint:contextcheck // The watchdog runs until it is stopped; ctx bounds the reads.
	end, err := superviseInstance(cmdCtx, inst, func() (InstanceCtx, error) {
		return reload(ctx, &pinned, cmdCtx.Integrity)
	}, output)
	if err != nil {
		return 0, err
	}

	return end.code(), nil
}

// supervisionEnd is how the supervision of an instance ended.
type supervisionEnd struct {
	// exit is the exit of the instance; nil when it did not start.
	exit *supervisor.Exit
	// stopped tells that the watchdog received a stop signal before the
	// instance exited.
	stopped bool
	// killed tells that the instance outlived its stop timeout and was
	// killed.
	killed bool
	// err is the failure that ended the supervision.
	err error
}

// record records what event says of the end of the supervision.
func (end *supervisionEnd) record(event supervisor.Event) {
	switch event := event.(type) {
	case supervisor.SignalReceived:
		if event.Action == supervisor.ActionStop && end.exit == nil {
			end.stopped = true
		}
	case supervisor.Killed:
		if event.Reason == supervisor.KillStopTimeout {
			end.killed = true
		}
	case supervisor.Exit:
		end.exit = &event
	}
}

// code returns the exit code of the watchdog of tt instance start
// --foreground that ended so.
func (end *supervisionEnd) code() int {
	switch {
	case end.err != nil:
		return watchdogFailure
	case end.killed:
		return ExitKilledOnStop
	case end.stopped, end.exit == nil:
		return 0
	case end.exit.State == nil:
		// The exit could not be waited for: nothing says the instance ended
		// well.
		return watchdogFailure
	}

	return ExitCodeOf(end.exit.State)
}

// foregroundSource is the Source of the watchdog of tt instance start
// --foreground: the Source of the watchdog that never restarts the
// instance.
type foregroundSource struct {
	*instanceSource
}

// Restart never restarts the instance.
func (foregroundSource) Restart(context.Context, supervisor.Exit) (bool, error) {
	return false, nil
}

// echoWriter writes the output of an instance into the log of its watchdog,
// tt.log, and a copy of it to echo.
type echoWriter struct {
	log  *watchdogLog
	echo io.Writer
}

// Write writes p into the log and to echo, and reports the write into the
// log.
func (w echoWriter) Write(p []byte) (int, error) {
	n, err := w.log.logger.Write(p)

	_, _ = w.echo.Write(p)

	return n, err
}

// asyncEcho copies the output of an instance to the standard output of its
// watchdog without ever blocking the instance: tt.log is where the output is
// kept, and an instance must not stall because the tt in the foreground does
// not read it, being suspended or writing to a terminal that is gone. What
// finds the buffer full is dropped from the copy.
type asyncEcho struct {
	out    io.Writer
	queue  chan []byte
	done   chan struct{}
	closed sync.Once
}

func newAsyncEcho(out io.Writer) *asyncEcho {
	echo := &asyncEcho{
		out:    out,
		queue:  make(chan []byte, echoBuffer),
		done:   make(chan struct{}),
		closed: sync.Once{},
	}

	go func() {
		defer close(echo.done)

		for chunk := range echo.queue {
			_, _ = echo.out.Write(chunk)
		}
	}()

	return echo
}

// Write queues a copy of chunk, or drops it when the queue is full.
func (echo *asyncEcho) Write(chunk []byte) (int, error) {
	select {
	case echo.queue <- append([]byte(nil), chunk...):
	default:
	}

	return len(chunk), nil
}

// close stops the copy once what is queued has been written, waiting for
// that no longer than timeout. No Write may follow.
func (echo *asyncEcho) close(timeout time.Duration) {
	echo.closed.Do(func() { close(echo.queue) })

	select {
	case <-echo.done:
	case <-time.After(timeout):
	}
}

// StartForegroundWatchdog starts the watchdog of instance for tt instance
// start --foreground: tt instance start --watchdog --foreground, the
// watchdog StartForeground runs, as a child of the current process that is
// not detached from it but leads a process group of its own. The standard
// input of the watchdog is the null device, its standard output and error
// go to stdout and stderr. args go before the arguments that run the
// watchdog. The caller waits for the watchdog and passes the stop signals on
// to it.
func StartForegroundWatchdog(cmdCtx *cmdcontext.CmdCtx, ttExecutable string,
	instance InstanceCtx, args []string, stdout, stderr io.Writer,
) (*exec.Cmd, error) {
	cmdArgs, err := watchdogCommandLine(cmdCtx, ttExecutable, &instance, args, true)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(context.Background(), ttExecutable, cmdArgs...)

	cmd.Stdin = nil
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// A signal the terminal sends to the group of the tt in the foreground
	// reaches the instance only as that tt passes it on.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err = cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("cannot start the watchdog for %s: %w",
			GetAppInstanceName(instance), err)
	}

	return cmd, nil
}
