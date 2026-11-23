package running

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tarantool/tt/v3/internal/supervisor"
)

// The watchdog of an instance publishes its live state in
// watchdogStateFileName in the run directory of the instance, for as long as
// it holds the pid file of the instance: its own pid and when it started, the
// process it runs, if one runs, with its pid and when it started, how many
// times the restart policy had it start the process again, and the last
// periodic integrity check. tt instance status shows the pid of that process,
// the one top, gdb, perf, box.info.pid and the log lines of tarantool name,
// rather than the pid of the watchdog, which the pid file holds.

const (
	// watchdogStateFileName is the file in the run directory of an instance
	// that holds the state its watchdog publishes, while the watchdog runs.
	watchdogStateFileName = "watchdog.json"
	// watchdogStateVersion is the version of the format of a WatchdogState:
	// a reader takes a state of another for one it cannot read.
	watchdogStateVersion = 1
)

var (
	// ErrNoWatchdogState reports a watchdog that publishes no state: there is
	// no state file, or the one there names another watchdog, which died
	// without removing it. A watchdog that a tt older than the state file
	// started publishes none.
	ErrNoWatchdogState = errors.New("the watchdog does not publish its state")
	// ErrWatchdogStateVersion is wrapped by the error for a state file of
	// another version of the format than watchdogStateVersion.
	ErrWatchdogStateVersion = errors.New("the state of the watchdog is of another format")
)

// WatchdogState is the live state of the watchdog of an instance, as the
// watchdog publishes it in the run directory of the instance.
type WatchdogState struct {
	// Version is watchdogStateVersion.
	Version int `json:"version"`
	// Watchdog is the watchdog that publishes the state, which holds the pid
	// file of the instance: a state that names another is a leftover.
	Watchdog StateProcess `json:"watchdog"`
	// Process is the process the watchdog runs, nil while none runs: before
	// the first start, during the restart delay and after the last exit.
	Process *StateProcess `json:"process"`
	// Restarts is how many times the watchdog started the process again, as
	// the restart policy of the instance says, since the watchdog started:
	// zero after the first start.
	Restarts int `json:"restarts"`
	// IntegrityCheck is the last periodic integrity check of the instance,
	// nil before the first one and without the checks.
	IntegrityCheck *IntegrityCheck `json:"integrity_check,omitempty"`
}

// StateProcess is a process of a WatchdogState: its pid and when it started.
type StateProcess struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// IntegrityCheck is a periodic integrity check of an instance: when it ran,
// and whether it passed, with the error it failed with otherwise. A failed
// check kills the process and ends the watchdog, whose last exit record
// (LastExit) keeps the error.
type IntegrityCheck struct {
	At    time.Time `json:"at"              yaml:"at"`
	OK    bool      `json:"ok"              yaml:"ok"`
	Error string    `json:"error,omitempty" yaml:"error,omitempty"`
}

// Describe tells how the check went, and when, in the time zone of the
// watchdog that ran it, as LastExit.Describe does.
func (check *IntegrityCheck) Describe() string {
	at := check.At.Format(time.DateTime)
	if check.OK {
		return "passed, at " + at
	}

	return fmt.Sprintf("failed, at %s: %s", at, check.Error)
}

// watchdogStateFile is the path of the state the watchdog of inst publishes.
func watchdogStateFile(inst *InstanceCtx) string {
	return filepath.Join(inst.RunDir, watchdogStateFileName)
}

// ReadWatchdogState returns the state the watchdog of inst publishes, the
// watchdog whose pid, watchdog, holds the pid file of inst (Status). A state
// file that names another watchdog is the leftover of one that died without
// removing it, and counts as none, as a missing file does: the error is
// ErrNoWatchdogState then. A state of another version of the format is an
// error that wraps ErrWatchdogStateVersion.
func ReadWatchdogState(inst *InstanceCtx, watchdog int) (*WatchdogState, error) {
	if inst.RunDir == "" || watchdog == 0 {
		return nil, ErrNoWatchdogState
	}

	data, err := os.ReadFile(watchdogStateFile(inst))

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNoWatchdogState
	case err != nil:
		return nil, fmt.Errorf("reading the state of the watchdog: %w", err)
	}

	// The version comes first: the rest of a state of another version may
	// not decode at all.
	var head struct {
		Version int `json:"version"`
	}

	err = json.Unmarshal(data, &head)
	if err != nil {
		return nil, fmt.Errorf("decoding the state of the watchdog: %w", err)
	}

	if head.Version != watchdogStateVersion {
		return nil, fmt.Errorf("%w: version %d, not %d", ErrWatchdogStateVersion, head.Version,
			watchdogStateVersion)
	}

	var state WatchdogState

	err = json.Unmarshal(data, &state)
	if err != nil {
		return nil, fmt.Errorf("decoding the state of the watchdog: %w", err)
	}

	if state.Watchdog.PID != watchdog {
		return nil, ErrNoWatchdogState
	}

	return &state, nil
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
