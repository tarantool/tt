package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"
)

var (
	errInvalidPID = errors.New("invalid pid ")
)

const (
	StartTestWorkerMsg = "Test worker started"
	StopTestWorkerMsg  = "Test worker stopped"
)

const (
	TestProcessLogPath = "test_process.log"
	TestProcessPidFile = "test_process.pid"
	processStartDelay  = 500 * time.Millisecond
)

// waitProcessChanges waits for the new process to set signal handlers.
func waitProcessChanges() {
	// We need to wait for the new process (tarantool instance) to set handlers.
	// It is necessary to update for more correct synchronization.
	time.Sleep(processStartDelay)
}

// cleanupDaemonFiles cleans up daemon artifacts.
func cleanupDaemonFiles(logFilename, pidFilename string) {
	_, err := os.Stat(logFilename)
	if !os.IsNotExist(err) {
		_ = os.Remove(logFilename)
	}

	_, err = os.Stat(pidFilename)
	if !os.IsNotExist(err) {
		_ = os.Remove(pidFilename)
	}
}

// readPID reads pid from filePath.
func readPID(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to open the PID file: %w", err)
	}

	buf := bytes.NewBufferString("")

	_, err = io.Copy(buf, file)
	if err != nil {
		return 0, fmt.Errorf("failed to read the PID file: %w", err)
	}

	pid, err := strconv.Atoi(buf.String())
	if err != nil {
		return 0, fmt.Errorf("failed to parse the PID: %w", err)
	}

	return pid, nil
}

// IsDaemonAlive checks is daemon alive by process pid.
func IsDaemonAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("%w%v", errInvalidPID, pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	err = proc.Signal(syscall.Signal(0))
	if err != nil {
		return false, fmt.Errorf("failed to signal process %d: %w", pid, err)
	}

	return true, nil
}
