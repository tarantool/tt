package daemon

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessBase(t *testing.T) {
	t.Cleanup(func() { cleanupDaemonFiles(TestProcessLogPath, TestProcessPidFile) })

	// Start daemon.
	cmd := exec.Command("go", "run", "test_process/test_process.go")
	err := cmd.Run()
	require.Nilf(t, err, `Can't start daemon. Error: "%v".`, err)

	// Check is daemon alive.
	waitProcessChanges()
	pid, err := readPID(TestProcessPidFile)
	require.Nilf(t, err, `Can't read daemon PID. Error: "%v".`, err)

	// Kill daemon if test fails.
	defer func() {
		syscall.Kill(pid, syscall.SIGINT)
		waitProcessChanges()
	}()

	alive, err := IsDaemonAlive(pid)
	require.Nilf(t, err, `Daemon is not alive. Error: "%v".`, err)
	require.True(t, alive, "Can't start daemon.")

	// Stop daemon.
	syscall.Kill(pid, syscall.SIGINT)

	// Check is daemon alive.
	waitProcessChanges()
	alive, _ = IsDaemonAlive(pid)
	require.False(t, alive, "Can't stop daemon.")

	_, err = os.Stat(TestProcessPidFile)
	require.NotNil(t, err, `Pid file still exists.`)

	// Check logger.
	testLog, err := os.Open(TestProcessLogPath)
	require.Nilf(t, err, `Can't open test log. Error: "%v".`, err)

	buf := bytes.NewBufferString("")
	_, err = io.Copy(buf, testLog)
	require.Nilf(t, err, `Can't read log output. Error: "%v".`, err)

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
