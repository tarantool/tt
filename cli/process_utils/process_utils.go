package process_utils

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Create a new directory.
// 0770:
// user:   read/write/execute
// group:  read/write/execute
// others: nil
const defaultDirPerms = 0770

const (
	ProcStateStopped = "NOT RUNNING."
	ProcStateDead    = "ERROR. The process is dead."
	ProcStateRunning = "RUNNING. PID: %v."
)

// GetPIDFromFile returns PID from the PIDFile.
func GetPIDFromFile(pidFileName string) (int, error) {
	if _, err := os.Stat(pidFileName); err != nil {
		return 0, fmt.Errorf(`Can't "stat" the PID file. Error: "%v".`, err)
	}

	pidFile, err := os.Open(pidFileName)
	if err != nil {
		return 0, fmt.Errorf(`Can't open the PID file. Error: "%v".`, err)
	}

	pidBytes, err := ioutil.ReadAll(pidFile)
	if err != nil {
		return 0, fmt.Errorf(`Can't read the PID file. Error: "%v".`, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		return 0,
			fmt.Errorf(`PID file exists with unknown format. Error: "%s"`, err)
	}

	return pid, nil
}

// CheckPIDFile checks that the process PID file exists
// and is readable. Or process is already exist.
// Removes PID file if process is dead.
func CheckPIDFile(pidFileName string) error {
	if _, err := os.Stat(pidFileName); err == nil {
		// The PID file already exists. We have to check if the process is alive.
		pid, err := GetPIDFromFile(pidFileName)
		if err != nil {
			return fmt.Errorf(`PID file exists, but PID can't be read. Error: "%v".`, err)
		}
		if res, _ := IsProcessAlive(pid); res {
			return fmt.Errorf("The process is already exists. PID: %d", pid)
		} else {
			os.Remove(pidFileName)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf(`Something went wrong while trying to read the PID file. Error: "%v".`,
			err)
	}

	return nil
}

// CreatePIDFile checks that the instance PID file is absent or
// deprecated and creates a new one. Returns an error on failure.
func CreatePIDFile(pidFileName string) error {
	if err := CheckPIDFile(pidFileName); err != nil {
		return err
	}

	pidAbsDir := filepath.Dir(pidFileName)
	if _, err := os.Stat(pidAbsDir); err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(pidAbsDir, defaultDirPerms)
			if err != nil {
				return fmt.Errorf(`can't crete PID file directory. Error: "%v".`, err)
			}
		} else {
			return fmt.Errorf(`can't stat PID file directory. Error: "%v".`, err)
		}
	}

	// Create a new PID file.
	// 0644:
	//    user:   read/write
	//    group:  read
	//    others: read
	pidFile, err := os.OpenFile(pidFileName,
		syscall.O_EXCL|syscall.O_CREAT|syscall.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf(`Can't create a new PID file. Error: "%v".`, err)
	}
	defer pidFile.Close()

	if _, err = pidFile.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		return err
	}

	return nil
}

// StopProcess stops the process by pidFile.
func StopProcess(pidFile string) (int, error) {
	pid, err := GetPIDFromFile(pidFile)
	if err != nil {
		return 0, err
	}

	alive, err := IsProcessAlive(pid)
	if !alive {
		return 0, fmt.Errorf(`The process is already dead. Error: "%v".`, err)
	}

	if err = syscall.Kill(pid, syscall.SIGINT); err != nil {
		return 0, fmt.Errorf(`Can't terminate the process. Error: "%v".`, err)
	}

	if res := waitProcessTermination(pid, 30*time.Second, 100*time.Millisecond); !res {
		return 0, fmt.Errorf("Can't terminate the process.")
	}

	return pid, nil
}

// ProcessStatus returns the status of the process.
func ProcessStatus(pidFile string) string {
	pid, err := GetPIDFromFile(pidFile)
	if err != nil {
		return fmt.Sprintf(ProcStateStopped)
	}

	alive, err := IsProcessAlive(pid)
	if !alive {
		return fmt.Sprintf(ProcStateDead)
	}

	return fmt.Sprintf(ProcStateRunning, pid)
}

// IsProcessAlive checks if the process is alive.
func IsProcessAlive(pid int) (bool, error) {
	// The signal 0 is used to check if a process is alive.
	// From `man 2 kill`:
	// If  sig  is  0,  then  no  signal is sent, but existence and permission
	// checks are still performed; this can be used to check for the existence
	// of  a  process  ID  or process group ID that the caller is permitted to
	// signal.
	if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
		return false, err
	}

	return true, nil
}

// waitProcessTermination waits while the process will be terminated.
// Returns true if the process was terminated and false if is steel alive.
func waitProcessTermination(pid int, timeout time.Duration,
	checkPeriod time.Duration) bool {
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
