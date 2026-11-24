package running

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tarantool/tt/v3/internal/supervisor"
)

// lastExitFileName is the record of how the last process of an instance
// ended, and when, in its run directory. The watchdog removes it once it holds
// the pid file of the instance, writes it at every exit of the process and at
// its own end, and leaves it behind: tt instance status --details of an
// instance that does not run tells from it why.
const lastExitFileName = "last_exit.json"

// LastExit is how the last process of an instance ended, as its watchdog
// recorded it.
type LastExit struct {
	// At is when the process exited, or the watchdog ended without one.
	At time.Time `json:"at" yaml:"at"`
	// Exited tells how the process exited (describeExit), empty when the
	// watchdog ended before one exited.
	Exited string `json:"exited,omitempty" yaml:"exited,omitempty"`
	// PID is the PID of the process that exited, 0 when the watchdog ended
	// before one exited, and in a record a tt before it wrote.
	PID int `json:"pid,omitempty" yaml:"pid,omitempty"`
	// CoreDumped tells that the process was killed by a signal and the
	// kernel dumped a core of it, as the crashes of the instance record it
	// (Crash).
	CoreDumped bool `json:"core_dumped,omitempty" yaml:"core_dumped,omitempty"`
	// Stopped tells that the process exited after a stop: tt instance stop,
	// restart, or a stop signal sent to the watchdog.
	Stopped bool `json:"stopped,omitempty" yaml:"stopped,omitempty"`
	// Error is the failure the watchdog ended with.
	Error string `json:"error,omitempty" yaml:"error,omitempty"`
}

// Describe tells how the process ended, and when, in the time zone of the
// watchdog that recorded it, the local time of the host.
func (last *LastExit) Describe() string {
	what := last.Exited
	if last.Stopped {
		what = "stopped, " + what
	}

	if last.Error != "" {
		if what != "" {
			what += "; "
		}

		what += "its watchdog ended: " + last.Error
	}

	if what == "" {
		what = "its watchdog ended"
	}

	return fmt.Sprintf("%s, at %s", what, last.At.Format(time.DateTime))
}

// Clean tells that the process ended as it was told to: stopped, with a
// zero exit code, and its watchdog with no error.
func (last *LastExit) Clean() bool {
	return last.Stopped && last.Error == "" && last.Exited == describeExitCode(0)
}

// lastExitFile is the path of the record of the last exit of inst.
func lastExitFile(inst *InstanceCtx) string {
	return filepath.Join(inst.RunDir, lastExitFileName)
}

// ReadLastExit returns the record of the last exit of the process of inst
// its watchdog left, and whether there is one.
func ReadLastExit(inst *InstanceCtx) (LastExit, bool) {
	var last LastExit

	if inst.RunDir == "" {
		return last, false
	}

	data, err := os.ReadFile(lastExitFile(inst))
	if err != nil {
		return last, false
	}

	err = json.Unmarshal(data, &last)
	if err != nil || last.At.IsZero() {
		return last, false
	}

	return last, true
}

// exitRecorder keeps the record of the last exit of the process of an
// instance up to date (lastExitFileName), and adds every exit by a signal to
// its crashes (recordCrash), which a new life of the instance keeps. It is
// used by the goroutine the watchdog runs on, and does nothing for an
// instance with no run directory.
type exitRecorder struct {
	inst *InstanceCtx
	// held tells that the watchdog holds the pid file of the instance.
	held bool
	// stopping tells that a stop signal came since the process started.
	stopping bool
	last     LastExit
	now      func() time.Time
	// warn writes a warning into the log of the watchdog, once it has
	// one: of a record of the crashes moved aside or not written
	// (recordCrash). nil writes nothing.
	warn func(message string)
}

// newExitRecorder returns the recorder of the last exit of inst.
func newExitRecorder(inst *InstanceCtx) *exitRecorder {
	return &exitRecorder{
		inst:     inst,
		held:     false,
		stopping: false,
		last: LastExit{
			At: time.Time{}, Exited: "", PID: 0, CoreDumped: false, Stopped: false, Error: "",
		},
		now:  time.Now,
		warn: nil,
	}
}

// onEvent records what event says of the last exit.
func (rec *exitRecorder) onEvent(event supervisor.Event) {
	if rec == nil || rec.inst.RunDir == "" {
		return
	}

	switch event := event.(type) {
	case supervisor.PidFileWritten:
		if event.Path == rec.inst.PIDFile && !rec.held {
			// A new life of the instance: what the last one left is over.
			rec.held = true
			_ = os.Remove(lastExitFile(rec.inst))
		}
	case supervisor.Started:
		rec.stopping = false
	case supervisor.SignalReceived:
		if event.Action == supervisor.ActionStop {
			rec.stopping = true
		}
	case supervisor.Exit:
		ended := rec.now()
		crash, crashed := crashOf(event, ended)

		rec.last = LastExit{
			At:         ended,
			Exited:     describeExit(event),
			PID:        event.Pid,
			CoreDumped: crash.CoreDumped,
			Stopped:    rec.stopping,
			Error:      "",
		}
		rec.write()

		// A process a stop ended by a signal, as tt instance kill --dump
		// ends one with SIGQUIT and a core, is a crash as well.
		if crashed {
			for _, warning := range recordCrash(rec.inst, crash, ended) {
				if rec.warn != nil {
					rec.warn(warning)
				}
			}
		}
	}
}

// end records err, the failure the watchdog ended with, if any. A watchdog
// that could not take the pid file records nothing: the instance may run
// under another one.
func (rec *exitRecorder) end(err error) {
	if rec == nil || err == nil || rec.inst.RunDir == "" {
		return
	}

	var runErr *supervisor.Error

	if !rec.held && errors.As(err, &runErr) && runErr.Op == supervisor.OpPidFile {
		return
	}

	if rec.last.At.IsZero() {
		rec.last.At = rec.now()
	}

	rec.last.Error = err.Error()
	rec.write()
}

// write writes the record (writeLastExit).
func (rec *exitRecorder) write() {
	writeLastExit(rec.inst, &rec.last)
}

// recordKilled records that tt instance kill killed inst, whose watchdog,
// killed with it, records nothing.
func recordKilled(inst *InstanceCtx) {
	if inst.RunDir == "" {
		return
	}

	writeLastExit(inst, &LastExit{
		At:         time.Now(),
		Exited:     "it was killed by tt instance kill",
		PID:        0,
		CoreDumped: false,
		Stopped:    false,
		Error:      "",
	})
}

// writeLastExit writes last as the record of the last exit of inst whole,
// through a file renamed into place, so that a reader never sees it half
// written. A record that cannot be written is none: the log of the watchdog
// has what it would say.
func writeLastExit(inst *InstanceCtx, last *LastExit) {
	data, err := json.Marshal(last)
	if err != nil {
		return
	}

	written, err := writeNewFile(inst.RunDir, "."+lastExitFileName+".*", data)
	if err != nil {
		return
	}

	err = os.Rename(written, lastExitFile(inst))
	if err != nil {
		_ = os.Remove(written)
	}
}
