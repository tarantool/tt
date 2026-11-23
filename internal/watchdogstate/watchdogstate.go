// Package watchdogstate is the format of the live state the watchdog of an
// instance publishes in FileName in the run directory of the instance, for
// as long as it holds the pid file of the instance: its own pid and when it
// started, the process it runs, if one runs, with its pid and when it
// started, how many times the restart policy had it start the process again,
// and the last periodic integrity check.
//
// The PID tt shows and prints for an instance is the pid of that process,
// the one top, gdb, perf, box.info.pid and the log lines of tarantool name,
// rather than the pid of the watchdog, which the pid file holds. The
// watchdog, in cli/running, writes the state; tt instance status, the
// commands that signal an instance and tt package, which cannot import
// cli/running, read it here.
package watchdogstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const (
	// FileName is the file in the run directory of an instance that holds
	// the state its watchdog publishes, while the watchdog runs.
	FileName = "watchdog.json"
	// Version is the version of the format of a State: a reader takes a
	// state of another for one it cannot read.
	Version = 1
)

var (
	// ErrNoState reports a watchdog that publishes no state: there is no
	// state file, or the one there names another watchdog, which died
	// without removing it. A watchdog that a tt older than the state file
	// started publishes none.
	ErrNoState = errors.New("the watchdog does not publish its state")
	// ErrVersion is wrapped by the error for a state file of another
	// version of the format than Version.
	ErrVersion = errors.New("the state of the watchdog is of another format")
)

// State is the live state of the watchdog of an instance, as the watchdog
// publishes it in the run directory of the instance.
type State struct {
	// Version is Version.
	Version int `json:"version"`
	// Watchdog is the watchdog that publishes the state, which holds the pid
	// file of the instance: a state that names another is a leftover.
	Watchdog Process `json:"watchdog"`
	// Process is the process the watchdog runs, nil while none runs: before
	// the first start, during the restart delay and after the last exit.
	Process *Process `json:"process"`
	// Restarts is how many times the watchdog started the process again, as
	// the restart policy of the instance says, since the watchdog started:
	// zero after the first start.
	Restarts int `json:"restarts"`
	// IntegrityCheck is the last periodic integrity check of the instance,
	// nil before the first one and without the checks.
	IntegrityCheck *IntegrityCheck `json:"integrity_check,omitempty"`
}

// Process is a process of a State: its pid and when it started.
type Process struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// IntegrityCheck is a periodic integrity check of an instance: when it ran,
// and whether it passed, with the error it failed with otherwise. A failed
// check kills the process and ends the watchdog, whose last exit record
// keeps the error.
type IntegrityCheck struct {
	At    time.Time `json:"at"              yaml:"at"`
	OK    bool      `json:"ok"              yaml:"ok"`
	Error string    `json:"error,omitempty" yaml:"error,omitempty"`
}

// Describe tells how the check went, and when, in the time zone of the
// watchdog that ran it, as the record of the last exit tells its exit.
func (check *IntegrityCheck) Describe() string {
	at := check.At.Format(time.DateTime)
	if check.OK {
		return "passed, at " + at
	}

	return fmt.Sprintf("failed, at %s: %s", at, check.Error)
}

// Path is the path of the state the watchdog of the instance whose run
// directory is runDir publishes.
func Path(runDir string) string {
	return filepath.Join(runDir, FileName)
}

// Read returns the state the watchdog publishes in runDir, the run directory
// of its instance, the watchdog whose pid, watchdog, holds the pid file of
// the instance. A state file that names another watchdog is the leftover of
// one that died without removing it, and counts as none, as a missing file
// does: the error is ErrNoState then. A state of another version of the
// format is an error that wraps ErrVersion.
func Read(runDir string, watchdog int) (*State, error) {
	if runDir == "" || watchdog == 0 {
		return nil, ErrNoState
	}

	data, err := os.ReadFile(Path(runDir))

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrNoState
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

	if head.Version != Version {
		return nil, fmt.Errorf("%w: version %d, not %d", ErrVersion, head.Version, Version)
	}

	var state State

	err = json.Unmarshal(data, &state)
	if err != nil {
		return nil, fmt.Errorf("decoding the state of the watchdog: %w", err)
	}

	if state.Watchdog.PID != watchdog {
		return nil, ErrNoState
	}

	return &state, nil
}

// ProcessPID returns the pid of the process the watchdog, whose pid is
// watchdog, runs, as it publishes it in runDir (Read); zero when that is not
// known: the watchdog publishes no state, as one an older tt started or one
// that is just starting, or no process runs now, as during the restart
// delay.
func ProcessPID(runDir string, watchdog int) int {
	state, err := Read(runDir, watchdog)
	if err != nil || state.Process == nil {
		return 0
	}

	return state.Process.PID
}
