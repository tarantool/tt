package running

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tarantool/tt/v3/internal/supervisor"
	"github.com/tarantool/tt/v3/internal/watchdogstate"
)

// The watchdog of an instance publishes its live state in
// watchdogStateFileName in the run directory of the instance, for as long as
// it holds the pid file of the instance: its own pid and when it started, the
// process it runs, if one runs, with its pid and when it started, how many
// times the restart policy had it start the process again, and the last
// periodic integrity check. tt instance status shows the pid of that process,
// the one top, gdb, perf, box.info.pid and the log lines of tarantool name,
// rather than the pid of the watchdog, which the pid file holds, and so do
// the messages of the commands that signal the instance (pidsOf). The format
// is the one of the watchdogstate package, which tt package reads as well.

const (
	// watchdogStateFileName is the file in the run directory of an instance
	// that holds the state its watchdog publishes, while the watchdog runs.
	watchdogStateFileName = watchdogstate.FileName
	// watchdogStateVersion is the version of the format of a WatchdogState:
	// a reader takes a state of another for one it cannot read.
	watchdogStateVersion = watchdogstate.Version
)

var (
	// ErrNoWatchdogState reports a watchdog that publishes no state: there is
	// no state file, or the one there names another watchdog, which died
	// without removing it. A watchdog that a tt older than the state file
	// started publishes none.
	ErrNoWatchdogState = watchdogstate.ErrNoState
	// ErrWatchdogStateVersion is wrapped by the error for a state file of
	// another version of the format than watchdogStateVersion.
	ErrWatchdogStateVersion = watchdogstate.ErrVersion
)

// WatchdogState is the live state of the watchdog of an instance, as the
// watchdog publishes it in the run directory of the instance.
type WatchdogState = watchdogstate.State

// StateProcess is a process of a WatchdogState: its pid and when it started.
type StateProcess = watchdogstate.Process

// IntegrityCheck is a periodic integrity check of an instance: when it ran,
// and whether it passed, with the error it failed with otherwise. A failed
// check kills the process and ends the watchdog, whose last exit record
// (LastExit) keeps the error.
type IntegrityCheck = watchdogstate.IntegrityCheck

// watchdogStateFile is the path of the state the watchdog of inst publishes.
func watchdogStateFile(inst *InstanceCtx) string {
	return watchdogstate.Path(inst.RunDir)
}

// ReadWatchdogState returns the state the watchdog of inst publishes, the
// watchdog whose pid, watchdog, holds the pid file of inst (Status). A state
// file that names another watchdog is the leftover of one that died without
// removing it, and counts as none, as a missing file does: the error is
// ErrNoWatchdogState then. A state of another version of the format is an
// error that wraps ErrWatchdogStateVersion (watchdogstate.Read).
func ReadWatchdogState(inst *InstanceCtx, watchdog int) (*WatchdogState, error) {
	return watchdogstate.Read(inst.RunDir, watchdog)
}

// instancePIDs are the pids the messages of the commands name an instance
// by: the process its watchdog runs, the PID tt instance status shows, and
// the watchdog, which holds the pid file of the instance.
type instancePIDs struct {
	// process is the process, as the watchdog publishes it; zero when that
	// is not known: the watchdog publishes no state, as one an older tt
	// started or one that is just starting, or no process runs now, as
	// during the restart delay.
	process int
	// watchdog is the watchdog, zero for one that holds the pid file and has
	// not written its pid yet.
	watchdog int
}

// pidsOf returns the pids of inst, whose watchdog is watchdog. A command that
// signals the watchdog reads them before the signal: the watchdog removes its
// state as it ends.
func pidsOf(inst *InstanceCtx, watchdog int) instancePIDs {
	return instancePIDs{
		process:  watchdogstate.ProcessPID(inst.RunDir, watchdog),
		watchdog: watchdog,
	}
}

// String names the instance by its process, " (PID = 123)", or, when that is
// not known, by its watchdog, labelled so, " (watchdog PID = 120)"; it is
// empty when neither is known.
func (pids instancePIDs) String() string {
	switch {
	case pids.process != 0:
		return fmt.Sprintf(" (PID = %d)", pids.process)
	case pids.watchdog != 0:
		return fmt.Sprintf(" (watchdog PID = %d)", pids.watchdog)
	default:
		return ""
	}
}

// withWatchdog names the instance by its process and its watchdog both,
// " (PID = 123, watchdog PID = 120)", for a message the user may have to act
// on by hand, such as a stop that failed; by the watchdog alone when the
// process is not known, as String does.
func (pids instancePIDs) withWatchdog() string {
	if pids.process == 0 || pids.watchdog == 0 {
		return pids.String()
	}

	return fmt.Sprintf(" (PID = %d, watchdog PID = %d)", pids.process, pids.watchdog)
}

// DescribePID returns what a message names inst by, whose watchdog, the
// owner of its pid file, is watchdog: " (PID = 123)", the process the
// watchdog runs, the PID tt instance status shows; " (watchdog PID = 120)"
// when the process is not known; empty when the watchdog is not known
// either (instancePIDs.String).
func DescribePID(inst *InstanceCtx, watchdog int) string {
	return pidsOf(inst, watchdog).String()
}

// removeWatchdogState removes the state file of inst that a watchdog which
// died without cleaning up left, while the caller holds the lock of the pid
// file of inst, so that no newer watchdog publishes it (removeLeftovers).
func removeWatchdogState(inst *InstanceCtx) {
	if inst.RunDir == "" {
		return
	}

	_ = os.Remove(watchdogStateFile(inst))
}

// statePublisher keeps the state of the watchdog of an instance up to date
// in the run directory of the instance (watchdogStateFileName): it publishes
// the state once the watchdog holds the pid file of the instance, at every
// start and every exit of the process and after every integrity check, and
// removes it before the watchdog gives up the pid file (unpublish). The
// supervisor reports its events from the goroutine of Run, and the periodic
// check runs on a goroutine of its own: the mutex keeps the state and its
// file whole and in order, whichever goroutine a change comes from.
type statePublisher struct {
	mu   sync.Mutex
	inst *InstanceCtx
	// held tells that the watchdog holds the pid file of the instance: the
	// state is published only meanwhile.
	held bool
	// starts is how many times the watchdog started the process.
	starts int
	state  WatchdogState
	now    func() time.Time
}

// newStatePublisher returns the publisher of the state of the watchdog of
// inst, which starts now.
func newStatePublisher(inst *InstanceCtx) *statePublisher {
	return &statePublisher{
		mu:     sync.Mutex{},
		inst:   inst,
		held:   false,
		starts: 0,
		state: WatchdogState{
			Version:        watchdogStateVersion,
			Watchdog:       StateProcess{PID: 0, StartedAt: time.Now()},
			Process:        nil,
			Restarts:       0,
			IntegrityCheck: nil,
		},
		now: time.Now,
	}
}

// onEvent publishes what event changes in the state. A publisher of an
// instance with no run directory does nothing.
func (pub *statePublisher) onEvent(event supervisor.Event) error {
	if pub.inst.RunDir == "" {
		return nil
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()

	switch event := event.(type) {
	case supervisor.PidFileWritten:
		if event.Path != pub.inst.PIDFile || pub.held {
			return nil
		}

		pub.held = true
		pub.state.Watchdog.PID = event.Pid
	case supervisor.Started:
		pub.starts++

		pub.state.Restarts = pub.starts - 1
		pub.state.Process = &StateProcess{PID: event.Pid, StartedAt: pub.now()}
	case supervisor.Exit:
		pub.state.Process = nil
	case supervisor.Checked:
		check := IntegrityCheck{At: pub.now(), OK: event.Err == nil, Error: ""}
		if event.Err != nil {
			check.Error = event.Err.Error()
		}

		pub.state.IntegrityCheck = &check
	default:
		return nil
	}

	if !pub.held {
		return nil
	}

	return pub.write()
}

// unpublish removes the state, before the watchdog gives up the pid file of
// the instance: a state the watchdog published counts only while it holds
// the file.
func (pub *statePublisher) unpublish() {
	pub.mu.Lock()
	defer pub.mu.Unlock()

	if !pub.held {
		return
	}

	pub.held = false

	_ = os.Remove(watchdogStateFile(pub.inst))
}

// write writes the state whole, through a file renamed into place, so that a
// reader finds the old state or the new one, never one half written. The
// file has the mode os.CreateTemp gives, launchFileMode, as the other files
// of the watchdog in the run directory have (writeNewFile).
func (pub *statePublisher) write() error {
	data, err := json.Marshal(&pub.state)
	if err != nil {
		return fmt.Errorf("encoding the state of the watchdog: %w", err)
	}

	dir := pub.inst.RunDir

	file, err := os.CreateTemp(dir, "."+watchdogStateFileName+".*")
	if err != nil {
		return fmt.Errorf("publishing the state of the watchdog: %w", err)
	}

	_, writeErr := file.Write(data)

	err = errors.Join(writeErr, file.Close())
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(dir, watchdogStateFileName))
	}

	if err != nil {
		_ = os.Remove(file.Name())

		return fmt.Errorf("publishing the state of the watchdog: %w", err)
	}

	return nil
}
