package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"syscall"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/tail"
)

var (
	// errAlreadyRunning is wrapped by the error for an instance
	// tt instance start --foreground is to run while it runs already.
	errAlreadyRunning = errors.New("already running")
	// errInstanceExited is wrapped by the error tt instance start
	// --foreground ends with for an instance that failed.
	errInstanceExited = errors.New("exited with code")
)

// foregroundStopSignals are the signals that stop tt instance start
// --foreground: it passes them on to the watchdogs of its instances, which
// stop their instances as they would on the signal (decision 31).
var foregroundStopSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}

// foregroundChild is the watchdog of an instance tt instance start
// --foreground runs.
type foregroundChild struct {
	name string
	cmd  *exec.Cmd
	// done tells that the watchdog has exited.
	done bool
}

// foregroundEnd is the exit of the watchdog of an instance.
type foregroundEnd struct {
	child *foregroundChild
	code  int
}

// startInstancesForeground runs instances in the foreground (decision 37):
// a watchdog for every one of them, which never restarts it
// (running.StartForegroundWatchdog), and tt itself in the foreground until
// all of them have exited.
//
// tt copies the output of the watchdogs to its own standard output and
// error, prefixed with the instance when there are several, and passes a
// stop signal it receives on to every watchdog that runs; SIGHUP goes on as
// well, which rotates the logs. The watchdogs lead process groups of their
// own, so a signal the terminal sends, such as the SIGINT of Ctrl-C, reaches
// the instances once, as tt passes it on. tt instance stop, kill, logrotate
// and status reach one watchdog from another terminal, through the pid file
// of its instance.
//
// An instance that ends, failed or stopped, does not stop the others: tt
// writes with what code it exited, as the exit code of its watchdog tells
// (running.StartForeground), and goes on. Once all have exited, tt exits
// with the code of the first that failed, a code other than 0, or with 0. A
// watchdog that tt has passed a stop signal on to and that a signal ended
// before it could stop its instance stopped as asked.
//
// An instance that runs already, in the background or in another tt, is an
// error before anything starts: a supervisor that runs tt must learn that
// it does not run what it asked for.
func startInstancesForeground(cmdCtx *cmdcontext.CmdCtx, instances []running.InstanceCtx) error {
	for i := range instances {
		state := running.Status(&instances[i])
		if state.Code == process_utils.ProcessRunningCode {
			name := running.GetAppInstanceName(instances[i])

			return fmt.Errorf("instance %s is %w (PID = %d); stop it with tt instance stop %s",
				name, errAlreadyRunning, state.PID, name)
		}
	}

	ttBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get the tt executable path: %w", err)
	}

	// A terminal that is gone must not end tt while its instances run: what
	// is written to it fails instead. SIGPIPE is caught rather than ignored,
	// which the watchdogs and their instances would inherit.
	brokenPipes := make(chan os.Signal, 1)

	signal.Notify(brokenPipes, syscall.SIGPIPE)
	defer signal.Stop(brokenPipes)

	signals := make(chan os.Signal, len(instances)+len(foregroundStopSignals))

	relayed := slices.Clone(foregroundStopSignals)
	if !signal.Ignored(syscall.SIGHUP) {
		relayed = append(relayed, syscall.SIGHUP)
	}

	signal.Notify(signals, relayed...)
	defer signal.Stop(signals)

	ends := make(chan foregroundEnd, len(instances))
	children := make([]*foregroundChild, 0, len(instances))
	nextColor := tail.DefaultColorPicker()

	var first *foregroundEnd

	for _, inst := range instances {
		name := running.GetAppInstanceName(inst)
		stdout, stderr := foregroundOutput(len(instances), name, nextColor)

		log.Infof("Starting an instance [%s] in the foreground...", name)

		cmd, err := running.StartForegroundWatchdog(cmdCtx, ttBin, inst,
			watchdogStartArgs(cmdCtx), stdout, stderr)
		if err != nil {
			log.Error(err.Error())

			if first == nil {
				first = &foregroundEnd{
					child: &foregroundChild{name: name, cmd: nil, done: true},
					code:  sdk.ExitFailure,
				}
			}

			continue
		}

		child := &foregroundChild{name: name, cmd: cmd, done: false}

		children = append(children, child)

		go func() {
			_ = cmd.Wait()

			ends <- foregroundEnd{child: child, code: running.ExitCodeOf(cmd.ProcessState)}
		}()
	}

	if end := superviseForeground(children, signals, ends); first == nil {
		first = end
	}

	if first == nil {
		return nil
	}

	// What tt exits with was written as the instance ended.
	return exitcode.Silent(sdk.Errorf(first.code, "instance %s %w %d", first.child.name,
		errInstanceExited, first.code))
}

// superviseForeground waits for the watchdogs children, passing the signals
// tt receives on to them, and returns the exit of the first that failed, or
// nil.
func superviseForeground(children []*foregroundChild, signals <-chan os.Signal,
	ends <-chan foregroundEnd,
) *foregroundEnd {
	var (
		first    *foregroundEnd
		stopping bool
	)

	for left := len(children); left > 0; {
		select {
		case sig := <-signals:
			if slices.Contains(foregroundStopSignals, sig) {
				stopping = true
			}

			for _, child := range children {
				if !child.done {
					_ = child.cmd.Process.Signal(sig)
				}
			}
		case end := <-ends:
			end.child.done = true
			left--

			if stopping && stoppedBySignal(end.child.cmd.ProcessState) {
				// A stop tt passed on ended the watchdog itself, before it
				// could take the signal: it stopped as asked.
				end.code = 0
			}

			if end.code != 0 || !stopping {
				_, _ = fmt.Fprintf(os.Stdout, "%s exited with code %d\n", end.child.name,
					end.code)
			}

			if end.code != 0 && first == nil {
				first = &end
			}
		}
	}

	return first
}

// stoppedBySignal reports whether a stop signal ended the process that
// ended as state says.
func stoppedBySignal(state *os.ProcessState) bool {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false
	}

	return slices.Contains(foregroundStopSignals, os.Signal(status.Signal()))
}

// foregroundOutput returns the writers the standard output and error of the
// watchdog of an instance go to: those of tt, as they are when the instance
// is alone, and prefixed with the instance, in a color of its own, when
// there are several.
//
// A watchdog writes into a pipe rather than the terminal: in a process group
// of its own it would be stopped for writing to the terminal on which tt is
// in the foreground, should the terminal say so. What tt cannot write is
// dropped, so that the pipe is always read and the watchdog never blocks.
func foregroundOutput(total int, name string, nextColor tail.ColorPicker) (io.Writer, io.Writer) {
	if total == 1 {
		return relayWriter{out: os.Stdout}, relayWriter{out: os.Stderr}
	}

	clr := nextColor()
	prefix := name + " "

	return running.NewColorizedPrefixWriter(os.Stdout, clr, prefix),
		running.NewColorizedPrefixWriter(os.Stderr, clr, prefix)
}

// relayWriter writes to out, ignoring the failures to.
type relayWriter struct {
	out io.Writer
}

// Write writes p to out and reports it written.
func (w relayWriter) Write(p []byte) (int, error) {
	_, _ = w.out.Write(p)

	return len(p), nil
}

// startForegroundWatchdog runs the watchdog of inst for tt instance start
// --foreground (running.StartForeground) and ends with the exit code that
// tells how the instance ended.
func startForegroundWatchdog(cmdCtx *cmdcontext.CmdCtx, inst *running.InstanceCtx) error {
	code, err := running.StartForeground(context.Background(), cmdCtx, inst, os.Stdout)
	if err != nil {
		return err
	}

	if code == 0 {
		return nil
	}

	// The watchdog logged a failure of its own; the exit of an instance is
	// for tt in the foreground to tell.
	return exitcode.Silent(sdk.Errorf(code, "instance %s %w %d",
		running.GetAppInstanceName(*inst), errInstanceExited, code))
}
