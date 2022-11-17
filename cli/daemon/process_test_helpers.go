package daemon

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"
)

const (
	StartTestWorkerMsg = "Test worker started"
	StopTestWorkerMsg  = "Test worker stopped"
)

const (
	TestProcessLogPath = "test_process.log"
	TestProcessPidFile = "test_process.pid"
)

// waitProcessChanges waits for the new process to set signal handlers.
func waitProcessChanges() {
	// We need to wait for the new process (tarantool instance) to set handlers.
	// It is necessary to update for more correct synchronization.
	time.Sleep(500 * time.Millisecond)
}

// cleanupDaemonFiles cleans up daemon artifacts.
func cleanupDaemonFiles(logFilename string, pidFilename string) {
	if _, err := os.Stat(logFilename); !os.IsNotExist(err) {
		os.Remove(logFilename)
	}

	if _, err := os.Stat(pidFilename); !os.IsNotExist(err) {
		os.Remove(pidFilename)
	}
}

// readPID reads pid from filePath.
func readPID(filePath string) (int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}

	buf := bytes.NewBufferString("")
	if _, err = io.Copy(buf, file); err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(buf.String())
	if err != nil {
		return 0, err
	}

	return pid, nil
}

// IsDaemonAlive checks is daemon alive by process pid.
func IsDaemonAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("invalid pid %v", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}

	err = proc.Signal(syscall.Signal(0))
	if err != nil {
		return false, err
	}

	return true, nil
}
