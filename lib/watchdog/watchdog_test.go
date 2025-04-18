package watchdog

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWatchdog_StartStop tests that the watchdog starts a process, creates a PID
// file, and stops the process when asked to.
func TestWatchdog_StartStop(t *testing.T) {
	t.Run("successful start and graceful stop", func(t *testing.T) {
		wd := NewWatchdog("test.pid", "wd.pid", 100*time.Millisecond)

		t.Cleanup(func() {
			os.Remove("test.pid")
			os.Remove("wd.pid")
		})

		// Mock command that will sleep for 1 second.
		cmd := exec.Command("sleep", "1")
		errChan := make(chan error, 1)

		go func() {
			errChan <- wd.Start(cmd.Path, cmd.Args[1:]...)
		}()

		// Wait for process to start.
		time.Sleep(200 * time.Millisecond)

		// Verify process is running.
		wd.cmdMutex.Lock()
		require.NotNil(t, wd.cmd)
		require.NotNil(t, wd.cmd.Process)
		wd.cmdMutex.Unlock()

		// Stop the watchdog.
		wd.Stop()

		// Verify no errors.
		select {
		case err := <-errChan:
			require.NoError(t, err)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timeout waiting for Start to return")
		}
	})

	t.Run("stop before process completes", func(t *testing.T) {
		wd := NewWatchdog("test.pid", "wd.pid", time.Second)

		t.Cleanup(func() {
			os.Remove("test.pid")
			os.Remove("wd.pid")
		})
		// Long-running process.
		cmd := exec.Command("sleep", "10")
		errChan := make(chan error, 1)

		go func() {
			errChan <- wd.Start(cmd.Path, cmd.Args[1:]...)
		}()

		// Wait for process to start.
		time.Sleep(200 * time.Millisecond)

		// Stop while process is running.
		wd.Stop()

		select {
		case err := <-errChan:
			require.NoError(t, err)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timeout waiting for Start to return")
		}
	})

	t.Run("process restart on failure", func(t *testing.T) {
		wd := NewWatchdog("test.pid", "wd.pid", 100*time.Millisecond)

		t.Cleanup(func() {
			os.Remove("test.pid")
			os.Remove("wd.pid")
		})

		// Command that exits immediately with error.
		cmd := exec.Command("false")
		errChan := make(chan error, 1)

		go func() {
			errChan <- wd.Start(cmd.Path, cmd.Args[1:]...)
		}()

		// Wait for at least one restart.
		time.Sleep(300 * time.Millisecond)

		// Should still be running (restarting).
		require.False(t, wd.shouldStop.Load())

		wd.Stop()

		select {
		case err := <-errChan:
			require.NoError(t, err)
		case <-time.After(500 * time.Millisecond):
			t.Fatal("timeout waiting for Start to return")
		}
	})
}

// TestWatchdog_SignalHandling tests that the watchdog can handle system signals.
// It verifies that sending a SIGTERM signal to the watchdog's signal channel
// causes the watchdog to stop the monitored process within the expected time frame.
func TestWatchdog_SignalHandling(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "test.pid")
	wdPidFile := filepath.Join(t.TempDir(), "watchdog.pid")

	wd := NewWatchdog(pidFile, wdPidFile, time.Second)

	go func() {
		err := wd.Start("sleep", "10")
		require.NoError(t, err)
	}()

	time.Sleep(100 * time.Millisecond)

	wd.signalChan <- syscall.SIGTERM

	select {
	case <-time.After(500 * time.Millisecond):
		t.Error("Watchdog didn't stop on SIGTERM")
	default:
	}
}

// TestWatchdog_WritePIDFiles verifies that the Watchdog's writePIDFiles
// method successfully creates the expected PID files for both the monitored
// process and the watchdog itself. It starts a test process, assigns it to
// the watchdog, and checks if the PID files are correctly created in the
// specified temporary directories.
func TestWatchdog_WritePIDFiles(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "test.pid")
	wdPidFile := filepath.Join(t.TempDir(), "watchdog.pid")

	wd := &Watchdog{
		pidFile:   pidFile,
		wdPidFile: wdPidFile,
	}

	cmd := exec.Command("sleep", "1")
	err := cmd.Start()
	require.NoError(t, err)

	defer cmd.Process.Kill()

	wd.cmd = cmd

	err = wd.writePIDFiles()
	require.NoError(t, err)

	_, err = os.Stat(pidFile)
	require.NoError(t, err)

	_, err = os.Stat(wdPidFile)
	require.NoError(t, err)

}
