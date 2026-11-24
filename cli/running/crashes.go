package running

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tarantool/tt/v3/internal/supervisor"
)

// The watchdog of an instance records every exit of its process by a signal,
// a crash, in crashesFileName in the run directory of the instance: when it
// was, the PID of the process, the signal and whether the kernel dumped a
// core of it. The record of the last exit (lastExitFileName) is the last
// exit alone, which the next life of the instance removes and every exit
// replaces: a crash that the restart policy, or Restart=on-failure of a
// systemd unit, followed with a start, and a stop later, is gone from it.
// The crashes stay, a life of the instance after another, the last
// crashesLimit of them, for tt instance status --details and
// tt coredump list, which looks for the cores.

const (
	// crashesFileName is the file in the run directory of an instance that
	// holds its crashes.
	crashesFileName = "crashes.json"
	// crashesVersion is the version of the format of the file: a reader
	// refuses a file of another, and the watchdog moves it aside
	// (recordCrash).
	crashesVersion = 1
	// crashesLimit is how many crashes the file keeps, the last ones.
	crashesLimit = 10
)

// Crash is an exit of the process of an instance by a signal, as its
// watchdog recorded it.
type Crash struct {
	// At is when the watchdog saw the process end, a moment after the
	// kernel dumped its core.
	At time.Time `json:"at" yaml:"at"`
	// PID is the PID of the process, the one of %p of kernel.core_pattern
	// and of the name of its core.
	PID int `json:"pid" yaml:"pid"`
	// Signal is the signal the process ended by.
	Signal syscall.Signal `json:"signal" yaml:"signal"`
	// CoreDumped tells that the kernel dumped a core of the process: to a
	// file, or to the program of a kernel.core_pattern pipe.
	CoreDumped bool `json:"core_dumped" yaml:"core_dumped"`
}

// Describe tells of the crash, and when it was, in the time zone of the
// watchdog that recorded it, the local time of the host.
func (crash *Crash) Describe() string {
	return fmt.Sprintf("PID %d, %s, at %s", crash.PID,
		describeSignal(crash.Signal, crash.CoreDumped), crash.At.Format(time.DateTime))
}

// crashes is the content of crashesFileName.
type crashes struct {
	// Version is crashesVersion.
	Version int `json:"version"`
	// Crashes are the crashes, the oldest first.
	Crashes []Crash `json:"crashes"`
}

var (
	// ErrCrashesNewer reports crashes of a version of the format after
	// crashesVersion: a newer tt wrote them.
	ErrCrashesNewer = errors.New("written by a newer tt")
	// errCrashesMalformed reports crashes that are no record of the format.
	errCrashesMalformed = errors.New("not a record of crashes")
)

// crashesFile is the path of the crashes of inst.
func crashesFile(inst *InstanceCtx) string {
	return filepath.Join(inst.RunDir, crashesFileName)
}

// ReadCrashes returns the crashes of the process of inst its watchdogs
// recorded, the oldest first; none for an instance with no record. A record
// that cannot be read is an error that says why: a file tt may not read, as
// one of another user, which the watchdog writes for its own alone, one that
// is no record of the format, or one of a newer version of it, which wraps
// ErrCrashesNewer.
func ReadCrashes(inst *InstanceCtx) ([]Crash, error) {
	if inst.RunDir == "" {
		return nil, nil
	}

	data, err := os.ReadFile(crashesFile(inst))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("cannot read the crashes: %w", err)
	}

	var read crashes

	err = json.Unmarshal(data, &read)

	switch {
	case err != nil:
		return nil, fmt.Errorf("cannot read the crashes of %s: %w: %w", crashesFile(inst),
			errCrashesMalformed, err)
	case read.Version > crashesVersion:
		return nil, fmt.Errorf("cannot read the crashes of %s: %w, of version %d of the format, "+
			"and this one reads %d", crashesFile(inst), ErrCrashesNewer, read.Version,
			crashesVersion)
	case read.Version != crashesVersion:
		return nil, fmt.Errorf("cannot read the crashes of %s: %w: version %d of the format",
			crashesFile(inst), errCrashesMalformed, read.Version)
	}

	return read.Crashes, nil
}

// LastCrash returns the last crash of the process of inst, and whether
// there is one, or the error of reading the crashes (ReadCrashes).
func LastCrash(inst *InstanceCtx) (Crash, bool, error) {
	all, err := ReadCrashes(inst)
	if len(all) == 0 {
		return Crash{At: time.Time{}, PID: 0, Signal: 0, CoreDumped: false}, false, err
	}

	return all[len(all)-1], true, nil
}

// crashOf returns the crash exit is, which ended the process at ended, and
// whether it is one: an exit by a signal.
func crashOf(exit supervisor.Exit, ended time.Time) (Crash, bool) {
	if exit.State == nil {
		return Crash{At: ended, PID: exit.Pid, Signal: 0, CoreDumped: false}, false
	}

	status, ok := exit.State.Sys().(syscall.WaitStatus)
	if !ok {
		return Crash{At: ended, PID: exit.Pid, Signal: 0, CoreDumped: false}, false
	}

	return crashOfStatus(exit.Pid, status, ended)
}

// crashOfStatus returns the crash of the process pid that ended with
// status, at ended, and whether it is one: an exit by a signal.
func crashOfStatus(pid int, status syscall.WaitStatus, ended time.Time) (Crash, bool) {
	if !status.Signaled() {
		return Crash{At: ended, PID: pid, Signal: 0, CoreDumped: false}, false
	}

	return Crash{
		At: ended, PID: pid, Signal: status.Signal(), CoreDumped: status.CoreDump(),
	}, true
}

// asideTimeLayout is the time in the name of a record of crashes
// recordCrash moves aside: crashes.json.20261124T100000.000000000Z.
const asideTimeLayout = "20060102T150405.000000000Z"

// recordCrash adds crash to the crashes of inst, the oldest dropped beyond
// crashesLimit, and writes them whole, through a file renamed into place, so
// that a reader never sees them half written. The watchdog of the instance,
// which holds its pid file, is the only writer. It returns what the log of
// the watchdog is to warn of, empty when there is nothing.
//
// A record that cannot be read is not replaced: it is moved aside, to
// crashes.json.<time>, in UTC, and the crashes are recorded anew, so that
// neither its history nor the crash is lost. A record of a newer tt, which
// a downgrade of tt finds, is the newer tt's to read once it is back, and a
// record that is no record of the format, or one the watchdog may not read,
// is the user's to look at; a tt that replaced them dropped the history
// without a word. A record that cannot be moved aside either is kept, and
// the crash is not recorded: the log has the exit, and so it has when the
// crashes cannot be written.
func recordCrash(inst *InstanceCtx, crash Crash, now time.Time) []string {
	recorded, readErr := ReadCrashes(inst)

	var warnings []string

	if readErr != nil {
		aside := crashesFile(inst) + "." + now.UTC().Format(asideTimeLayout)

		err := os.Rename(crashesFile(inst), aside)
		if err != nil {
			return []string{fmt.Sprintf("%v; it cannot be moved aside either, and the crash of "+
				"PID %d is not recorded: %v", readErr, crash.PID, err)}
		}

		warnings = append(warnings, fmt.Sprintf("%v; it is moved to %s, and the crashes are "+
			"recorded anew", readErr, aside))
	}

	recorded = append(recorded, crash)

	err := writeCrashes(inst, recorded[max(len(recorded)-crashesLimit, 0):])
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("cannot record the crash of PID %d: %v",
			crash.PID, err))
	}

	return warnings
}

// writeCrashes writes all, the crashes of inst, through a file renamed into
// place.
func writeCrashes(inst *InstanceCtx, all []Crash) error {
	data, err := json.Marshal(&crashes{Version: crashesVersion, Crashes: all})
	if err != nil {
		return fmt.Errorf("cannot encode the crashes: %w", err)
	}

	file, err := os.CreateTemp(inst.RunDir, "."+crashesFileName+".*")
	if err != nil {
		return fmt.Errorf("cannot write the crashes: %w", err)
	}

	_, err = file.Write(data)

	err = errors.Join(err, file.Close())
	if err == nil {
		err = os.Rename(file.Name(), crashesFile(inst))
	}

	if err != nil {
		_ = os.Remove(file.Name())

		return fmt.Errorf("cannot write the crashes: %w", err)
	}

	return nil
}
