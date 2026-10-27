package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessBase(t *testing.T) {
	t.Cleanup(func() { cleanupDaemonFiles(TestProcessLogPath, TestProcessPidFile) })

	// Start daemon.
	cmd := exec.CommandContext(t.Context(), "go", "run", "test_process/test_process.go")
	err := cmd.Run()
	require.NoErrorf(t, err, `Can't start daemon. Error: "%v".`, err)

	// Check is daemon alive.
	waitProcessChanges()

	pid, err := readPID(TestProcessPidFile)
	require.NoErrorf(t, err, `Can't read daemon PID. Error: "%v".`, err)

	// Kill daemon if test fails.
	defer func() {
		_ = syscall.Kill(pid, syscall.SIGINT)

		waitProcessChanges()
	}()

	alive, err := isDaemonAlive(pid)
	require.NoErrorf(t, err, `Daemon is not alive. Error: "%v".`, err)
	require.True(t, alive, "Can't start daemon.")

	// Stop daemon.
	_ = syscall.Kill(pid, syscall.SIGINT)

	// Check is daemon alive.
	waitProcessChanges()

	alive, _ = isDaemonAlive(pid)
	require.False(t, alive, "Can't stop daemon.")

	_, err = os.Stat(TestProcessPidFile)
	require.Error(t, err, `Pid file still exists.`)

	// Check logger.
	testLog, err := os.Open(TestProcessLogPath)
	require.NoErrorf(t, err, `Can't open test log. Error: "%v".`, err)

	buf := bytes.NewBufferString("")

	_, err = io.Copy(buf, testLog)
	require.NoErrorf(t, err, `Can't read log output. Error: "%v".`, err)

	logContent := buf.String()
	msgIdx1 := strings.Index(buf.String(), startDaemonMsg)
	require.NotEqual(t, -1, msgIdx1,
		"The message in the log is different from what was expected: %v", logContent)

	msgIdx2 := strings.Index(buf.String(), StartTestWorkerMsg)
	require.NotEqual(t, -1, msgIdx2,
		"The message in the log is different from what was expected: %v", logContent)
	require.Greater(t, msgIdx2, msgIdx1,
		"The message in the log is different from what was expected: %v", logContent)

	msgIdx3 := strings.Index(buf.String(), stopDaemonMsg)
	require.NotEqual(t, -1, msgIdx3,
		"The message in the log is different from what was expected: %v", logContent)
	require.Greater(t, msgIdx3, msgIdx2,
		"The message in the log is different from what was expected: %v", logContent)

	msgIdx4 := strings.Index(buf.String(), StopTestWorkerMsg)
	require.NotEqual(t, -1, msgIdx4,
		"The message in the log is different from what was expected: %v", logContent)
	require.Greater(t, msgIdx4, msgIdx3,
		"The message in the log is different from what was expected: %v", logContent)
}

var errInvalidPID = errors.New("invalid pid ")

const processStartDelay = 500 * time.Millisecond

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

// isDaemonAlive checks is daemon alive by process pid.
func isDaemonAlive(pid int) (bool, error) {
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
