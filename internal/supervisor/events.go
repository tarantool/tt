package supervisor

import (
	"os"
	"syscall"
	"time"
)

// Event is something that happened during the supervision. Options.OnEvent
// receives one of the types below; a consumer switches on the type to write
// its own log lines.
type Event interface {
	event()
}

// PidFileWritten reports a pid file written: the supervisor's before the
// first start, the child's after every start.
type PidFileWritten struct {
	Path string
	Pid  int
}

// Started reports a running child. Its pid file, if any, is already written.
type Started struct {
	Pid int
}

// SignalAction is what the engine did with a received signal.
type SignalAction int

const (
	// ActionStop stopped the child, or ended the restart delay.
	ActionStop SignalAction = iota + 1
	// ActionReload ran the reload hook and forwarded the signal to the child,
	// if there was one.
	ActionReload
	// ActionForward forwarded the signal to the child.
	ActionForward
	// ActionDrop dropped the signal: no child was running.
	ActionDrop
	// ActionIgnore dropped the signal: Options.IgnoreSignals has it.
	ActionIgnore
)

func (action SignalAction) String() string {
	switch action {
	case ActionStop:
		return "stop"
	case ActionReload:
		return "reload"
	case ActionForward:
		return "forward"
	case ActionDrop:
		return "drop"
	case ActionIgnore:
		return "ignore"
	}

	return "unknown"
}

// SignalReceived reports a received signal and what was done with it. Err
// holds a failure of the reload hook or of the delivery to the child.
type SignalReceived struct {
	Signal syscall.Signal
	Action SignalAction
	Err    error
}

// KillReason tells why the child was killed with SIGKILL.
type KillReason int

const (
	// KillStopTimeout: the child outlived Spec.StopTimeout after a stop.
	KillStopTimeout KillReason = iota + 1
	// KillCheckFailed: Options.Check returned an error.
	KillCheckFailed
	// KillChildPidFile: the child pid file could not be written.
	KillChildPidFile
)

func (reason KillReason) String() string {
	switch reason {
	case KillStopTimeout:
		return "stop timeout"
	case KillCheckFailed:
		return "check failed"
	case KillChildPidFile:
		return "child pid file"
	}

	return "unknown"
}

// Killed reports SIGKILL sent to the child, or to its process group. Err is
// the failure to send it.
type Killed struct {
	Pid    int
	Reason KillReason
	Err    error
}

// Exit reports a child that exited and was waited for. State is the state of
// the exited process, nil only if waiting for it failed. Err is what
// exec.Cmd.Wait returned: nil for a zero exit status, an *exec.ExitError for
// any other exit, or the failure to wait or to copy the child's input or
// output.
type Exit struct {
	Pid   int
	State *os.ProcessState
	Err   error
}

// Restarting reports the restart delay beginning.
type Restarting struct {
	Delay time.Duration
}

// Checked reports a finished periodic check. A non-nil Err is a failed check.
type Checked struct {
	Err error
}

func (PidFileWritten) event() {}
func (Started) event()        {}
func (SignalReceived) event() {}
func (Killed) event()         {}
func (Exit) event()           {}
func (Restarting) event()     {}
func (Checked) event()        {}
