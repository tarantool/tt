package process_utils

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
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
)

const (
	processTerminationTimeout = 30 * time.Second
	processPollInterval       = 100 * time.Millisecond
)

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

// GetPIDFromFile returns PID from the PIDFile.
func GetPIDFromFile(pidFileName string) (int, error) {
	_, err := os.Stat(pidFileName)
	if err != nil {
		return 0, fmt.Errorf(`can't "stat" the PID file. Error: %w`, err)
	}

	pidFile, err := os.Open(pidFileName)
	if err != nil {
		return 0, fmt.Errorf(`can't open the PID file. Error: "%w"`, err)
	}

	defer func() {
		_ = pidFile.Close()
	}()

	pidBytes, err := io.ReadAll(pidFile)
	if err != nil {
		return 0, fmt.Errorf(`can't read the PID file. Error: "%w"`, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return 0,
			fmt.Errorf(`pID file exists with unknown format. Error: "%w"`, err)
	}

	return pid, nil
}

// CheckPIDFile reports an error for a PID file that names a live process, or
// that cannot be read; a missing file, or one naming a dead process, is no
// error. It never removes a stale file: CreatePIDFile takes one over under
// the lock.
func CheckPIDFile(pidFileName string) error {
	_, err := os.Stat(pidFileName)
	if err == nil {
		// The PID file already exists. We have to check if the process is alive.
		pid, err := GetPIDFromFile(pidFileName)
		if err != nil {
			return fmt.Errorf(`pID file exists, but PID can't be read. Error: "%w"`, err)
		}

		if res, _ := IsProcessAlive(pid); res {
			return fmt.Errorf("%w%d", errTheProcessAlreadyExistsPID, pid)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf(`something went wrong while trying to read the PID file. Error: "%w"`,
			err)
	}

	return nil
}

// ExistsAndRecord checks if the process with the given pidFileName exists and is alive.
// If it does, returns true, otherwise returns false.
// If something went wrong while trying to read the PID file, returns an error.
func ExistsAndRecord(pidFileName string) (bool, error) {
	_, err := os.Stat(pidFileName)
	if err == nil {
		// The PID file already exists. We have to check if the process is alive.
		pid, err := GetPIDFromFile(pidFileName)
		if err != nil {
			return false, fmt.Errorf(`PID file exists, but PID can't be read. Error: "%w"`, err)
		}

		if res, _ := IsProcessAlive(pid); res {
			return true, nil
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf(`something went wrong while trying to read the`+
			`PID file. Error: "%w"`, err)
	}

	return false, nil
}

// CreatePIDFile takes the PID file over for pid and writes pid into it, as
// pidfile.Acquire does. The caller owns the file until it releases it, or
// keeps it when it exits while pid goes on.
func CreatePIDFile(pidFileName string, pid int) (*pidfile.File, error) {
	return pidfile.Acquire(pidFileName, pid)
}

// getRunningPid returns PID from pidfile and check the process is running.
func getRunningPid(pidFile string) (int, error) {
	pid, err := GetPIDFromFile(pidFile)
	if err != nil {
		return 0, err
	}

	alive, err := IsProcessAlive(pid)
	if err != nil {
		return 0, fmt.Errorf("failed to check if the process %v is running: %w", pid, err)
	}

	if !alive {
		return 0, fmt.Errorf("%w%v is not running", errTheProcessIsNotRunning, pid)
	}

	return pid, nil
}

// StopProcess stops the process by pidFile.
func StopProcess(pidFile string) (int, error) {
	pid, err := getRunningPid(pidFile)
	if err != nil {
		return 0, fmt.Errorf("can't get pid of running process: %w", err)
	}

	err = syscall.Kill(pid, syscall.SIGINT)
	if err != nil {
		return 0, fmt.Errorf(`can't terminate the process. Error: "%w"`, err)
	}

	if res := waitProcessTermination(pid, processTerminationTimeout, processPollInterval); !res {
		return 0, errProcessTermination
	}

	return pid, nil
}

// QuitProcess quits the process by pidFile.
func QuitProcess(pidFile string) (int, error) {
	pid, err := getRunningPid(pidFile)
	if err != nil {
		return 0, fmt.Errorf("can't get pid of running process: %w", err)
	}

	err = syscall.Kill(pid, syscall.SIGQUIT)
	if err != nil {
		return 0, fmt.Errorf("can't terminate the process with SIGQUIT: %w", err)
	}

	if res := waitProcessTermination(pid, processTerminationTimeout, processPollInterval); !res {
		return 0, errProcessSIGQUIT
	}

	return pid, nil
}

// KillProcessGroup kills a process group using a PID from the pid file as a process group id.
func KillProcessGroup(pidFile string) (int, error) {
	pid, err := getRunningPid(pidFile)
	if err != nil {
		return 0, fmt.Errorf("can't get pid of running process: %w", err)
	}

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return 0, fmt.Errorf("can't get a process group of the %d process: %w", pid, err)
	}

	err = syscall.Kill(-pgid, syscall.SIGKILL)
	if err != nil {
		return 0, fmt.Errorf("can't kill the process: %w", err)
	}

	return pid, nil
}

// ProcessStatus returns the status of the process.
func ProcessStatus(pidFile string) ProcessState {
	pid, err := GetPIDFromFile(pidFile)
	if err != nil {
		return ProcStateStopped
	}

	alive, _ := IsProcessAlive(pid)
	if !alive {
		return ProcStateDead
	}

	procState := ProcStateRunning

	procState.PID = pid

	return procState
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
