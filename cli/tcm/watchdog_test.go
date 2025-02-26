package tcm

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWatchdogStartProcess(t *testing.T) {
	watchdog, err := NewWatchdog(1 * time.Second)
	require.NoError(t, err)

	go func() {
		watchdog.Start("sleep", "5")
		require.NoError(t, err)
	}()

	time.Sleep(2 * time.Second)

	_, err = os.Stat(watchdog.pidFile)
	require.NoError(t, err)

	watchdog.Stop()
}

func TestWatchdogRestartProcess(t *testing.T) {
	watchdog, err := NewWatchdog(1 * time.Second)
	require.NoError(t, err)

	go func() {
		err := watchdog.Start("sleep", "1")
		require.NoError(t, err)
	}()

	time.Sleep(3 * time.Second)

	_, err = os.Stat(watchdog.pidFile)
	require.NoError(t, err)

	watchdog.Stop()
}

func TestWritePIDToFile(t *testing.T) {
	pidFile := "/tmp/watchdog_test.pid"
	defer os.Remove(pidFile)

	cmd := exec.Command("sleep", "1")
	err := cmd.Start()
	require.NoError(t, err)
	defer cmd.Process.Kill()

	watchdog := &Watchdog{
		cmd:     cmd,
		pidFile: pidFile,
	}

	err = watchdog.writePIDToFile()
	require.NoError(t, err)

	pidData, err := os.ReadFile(pidFile)
	require.NoError(t, err)

	expectedPID := fmt.Sprintf("%d", cmd.Process.Pid)
	require.Equal(t, expectedPID, string(pidData))
}
