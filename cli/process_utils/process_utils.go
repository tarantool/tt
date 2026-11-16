package process_utils

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/tarantool/tt/v3/internal/pidfile"
)

var (
	errProcessTermination         = errors.New("can't terminate the process")
	errProcessSIGQUIT             = errors.New("can't terminate the process with SIGQUIT")
	errTheProcessAlreadyExistsPID = errors.New("the process already exists. PID: ")
	errTheProcessIsNotRunning     = errors.New("the process ")
	// errNoPid is wrapped by the error for a pid that names no single
	// process: 0 and -1 would signal a whole group or every process.
	errNoPid = errors.New("not the pid of a process")
	// errProcessStarting is wrapped by the error for a pid file whose owner
	// holds it without a pid yet: it is starting.
	errProcessStarting = errors.New("the process is starting and has not written its pid yet")
	// errNoRunningProcess is wrapped by the error for a pid file that is
	// not there.
	errNoRunningProcess = errors.New("no running process: ")
)

// TerminationTimeout is how long StopProcess and QuitProcess wait for the
// process to exit. A watchdog they stop kills its child 30 seconds after the
// stop signal and then reaps it and cleans up, so they wait longer than that.
const TerminationTimeout = 35 * time.Second

const processPollInterval = 100 * time.Millisecond

type ProcessState struct {
	Code        int
	colorSprint func(a ...any) string
	Status      string
	PID         int
}

const (
	ProcessRunningCode = iota
	ProcessStoppedCode
	ProcessDeadCode
)

var (
	ProcStateRunning = ProcessState{
		Code:        ProcessRunningCode,
		colorSprint: color.New(color.FgGreen).SprintFunc(),
		Status:      "RUNNING",
		PID:         0,
	}
	ProcStateStopped = ProcessState{
		Code:        ProcessStoppedCode,
		colorSprint: color.New(color.FgYellow).SprintFunc(),
		Status:      "NOT RUNNING",
		PID:         0,
	}
	ProcStateDead = ProcessState{
		Code:        ProcessDeadCode,
		colorSprint: color.New(color.FgRed).SprintFunc(),
		Status:      "ERROR. The process is dead",
		PID:         0,
	}
)

// FormattedStatus returns the status string formatted with the appropriate color.
func (procState ProcessState) FormattedStatus() string {
	return procState.colorSprint(procState.Status)
}

// String makes a string from ProcessState.
func (procState ProcessState) String() string {
	if procState.Code == ProcessRunningCode {
		return fmt.Sprintf("%s. PID: %d.", procState.Status, procState.PID)
	}

	return procState.Status
}

// CheckPIDFile reports an error for a PID file that a running process owns,
// holding its lock (pidfile.Owner), or that cannot be read; a missing file,
// or a stale one nobody holds, is no error, whatever process the pid in it
// names by now. It never removes a stale file: CreatePIDFile takes one over
// under the lock.
func CheckPIDFile(pidFileName string) error {
	pid, owned, err := pidfile.Owner(pidFileName)

	switch {
	case err != nil:
		return fmt.Errorf("cannot check the PID file: %w", err)
	case owned && pid == 0:
		return fmt.Errorf("%w: %s", errProcessStarting, pidFileName)
	case owned:
		return fmt.Errorf("%w%d", errTheProcessAlreadyExistsPID, pid)
	}

	return nil
}

// OwnerStatus returns the state of the process that owns the pid file at
// path, judged by the lock of the file alone (pidfile.Owner): RUNNING, with
// the pid in the file, while a process holds the lock, which the owner of a
// pid file does for its whole life; NOT RUNNING without a file, or with one
// that holds no pid; and the dead state, with the pid in it, for a file
// nobody holds, whose owner died without removing it. The pid in such a
// file is not asked about, since it may name an unrelated process by now.
// A file that cannot be read counts as none.
func OwnerStatus(path string) ProcessState {
	pid, owned, err := pidfile.Owner(path)

	switch {
	case err != nil, !owned && pid == 0:
		return ProcStateStopped
	case !owned:
		state := ProcStateDead

		state.PID = pid

		return state
	}

	state := ProcStateRunning

	state.PID = pid

	return state
}

// ExistsAndRecord reports whether a running process owns the pid file
// pidFileName, holding its lock (pidfile.Owner): a stale file names no
// running process, whatever process its pid names by now. If something went
// wrong while trying to read the PID file, returns an error.
func ExistsAndRecord(pidFileName string) (bool, error) {
	_, owned, err := pidfile.Owner(pidFileName)
	if err != nil {
		return false, fmt.Errorf("cannot check the PID file: %w", err)
	}

	return owned, nil
}

// CreatePIDFile takes the PID file over for pid and writes pid into it, as
// pidfile.Acquire does. The caller owns the file until it releases it, or
// keeps it when it exits while pid goes on.
func CreatePIDFile(pidFileName string, pid int) (*pidfile.File, error) {
	return pidfile.Acquire(pidFileName, pid)
}

// ReservePIDFile takes the PID file over before the pid it is to hold is
// known, as pidfile.Reserve does, for a process about to be started that
// inherits its lock.
func ReservePIDFile(pidFileName string) (*pidfile.File, error) {
	return pidfile.Reserve(pidFileName)
}

// getRunningPid returns the pid of the process that owns pidFile, holding
// its lock (pidfile.Owner). A file nobody holds names no running process,
// whatever process its pid names by now: its owner is gone.
func getRunningPid(pidFile string) (int, error) {
	pid, owned, err := pidfile.Owner(pidFile)

	switch {
	case err != nil:
		return 0, fmt.Errorf("cannot check the PID file: %w", err)
	case !owned && pid == 0:
		return 0, fmt.Errorf("%w%s: %w", errNoRunningProcess, pidFile, fs.ErrNotExist)
	case !owned:
		return 0, fmt.Errorf("%w%v is not running", errTheProcessIsNotRunning, pid)
	case pid == 0:
		return 0, fmt.Errorf("%w: %s", errProcessStarting, pidFile)
	}

	return pid, nil
}

// InterruptProcess sends SIGINT to the process that owns pidFile, holding its
// lock, without waiting for it to exit.
func InterruptProcess(pidFile string) (int, error) {
	pid, err := getRunningPid(pidFile)
	if err != nil {
		return 0, fmt.Errorf("can't get pid of running process: %w", err)
	}

	err = Interrupt(pid)
	if err != nil {
		return 0, err
	}

	return pid, nil
}

// Interrupt sends SIGINT to the process pid without waiting for it to exit.
// The caller tells that pid is the process it means: a pid read from a pid
// file may name another process by now.
func Interrupt(pid int) error {
	err := checkPid(pid)
	if err != nil {
		return err
	}

	err = syscall.Kill(pid, syscall.SIGINT)
	if err != nil {
		return fmt.Errorf(`can't terminate the process. Error: "%w"`, err)
	}

	return nil
}

// checkPid refuses a pid that names no single process.
func checkPid(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("%w: %d", errNoPid, pid)
	}

	return nil
}

// WaitProcessTermination waits up to timeout for the process to exit.
func WaitProcessTermination(pid int, timeout time.Duration) error {
	if !waitProcessTermination(pid, timeout, processPollInterval) {
		return errProcessTermination
	}

	return nil
}

// StopProcess stops the process by pidFile, waiting TerminationTimeout for it
// to exit.
func StopProcess(pidFile string) (int, error) {
	return StopProcessWithin(pidFile, TerminationTimeout)
}

// StopProcessWithin sends SIGINT to the process by pidFile and waits up to
// timeout for it to exit.
func StopProcessWithin(pidFile string, timeout time.Duration) (int, error) {
	pid, err := InterruptProcess(pidFile)
	if err != nil {
		return 0, err
	}

	err = WaitProcessTermination(pid, timeout)
	if err != nil {
		return 0, err
	}

	return pid, nil
}

// QuitProcess sends SIGQUIT to the process pid and waits TerminationTimeout
// for it to exit. The caller tells that pid is the process it means, as for
// Interrupt.
func QuitProcess(pid int) error {
	err := checkPid(pid)
	if err != nil {
		return err
	}

	err = syscall.Kill(pid, syscall.SIGQUIT)
	if err != nil {
		return fmt.Errorf("can't terminate the process with SIGQUIT: %w", err)
	}

	if res := waitProcessTermination(pid, TerminationTimeout, processPollInterval); !res {
		return errProcessSIGQUIT
	}

	return nil
}

// KillProcessGroup kills the process group of the process pid with SIGKILL.
// The caller tells that pid is the process it means, as for Interrupt.
func KillProcessGroup(pid int) error {
	err := checkPid(pid)
	if err != nil {
		return err
	}

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return fmt.Errorf("can't get a process group of the %d process: %w", pid, err)
	}

	err = syscall.Kill(-pgid, syscall.SIGKILL)
	if err != nil {
		return fmt.Errorf("can't kill the process: %w", err)
	}

	return nil
}

// IsProcessAlive checks if the process is alive.
func IsProcessAlive(pid int) (bool, error) {
	// The signal 0 is used to check if a process is alive.
	// From `man 2 kill`:
	// If  sig  is  0,  then  no  signal is sent, but existence and permission
	// checks are still performed; this can be used to check for the existence
	// of  a  process  ID  or process group ID that the caller is permitted to
	// signal.
	err := syscall.Kill(pid, syscall.Signal(0))
	if err != nil {
		return false, fmt.Errorf("probing with signal 0: %w", err)
	}

	return true, nil
}

// waitProcessTermination waits while the process will be terminated.
// Returns true if the process was terminated and false if is steel alive.
func waitProcessTermination(pid int, timeout time.Duration,
	checkPeriod time.Duration,
) bool {
	if res, _ := IsProcessAlive(pid); !res {
		return true
	}

	result := false
	breakTimer := time.NewTimer(timeout)

loop:
	for {
		select {
		case <-breakTimer.C:
			if res, _ := IsProcessAlive(pid); !res {
				result = true
			}

			break loop
		case <-time.After(checkPeriod):
			if res, _ := IsProcessAlive(pid); !res {
				result = true
				break loop
			}
		}
	}

	return result
}
