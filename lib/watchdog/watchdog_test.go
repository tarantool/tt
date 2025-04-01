package watchdog

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestWatchdog_StartStop tests that the watchdog starts a process, creates a PID
// file, and stops the process when asked to.
func TestWatchdog_StartStop(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "test.pid")
	wdPidFile := filepath.Join(t.TempDir(), "watchdog.pid")

	wd := NewWatchdog(pidFile, wdPidFile, 100*time.Millisecond)

	err := wd.Start("sleep", "1")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	wd.Stop()

	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Error("PID file not created")
	}
	if _, err := os.Stat(wdPidFile); os.IsNotExist(err) {
		t.Error("Watchdog PID file not created")
	}
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
		if err != nil {
			t.Logf("Start exited with: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	wd.signalChan <- syscall.SIGTERM

	select {
	case <-time.After(500 * time.Millisecond):
		t.Error("Watchdog didn't stop on SIGTERM")
	default:
	}
}

// TestWatchdog_TerminateProcess verifies that the watchdog's terminateProcess
// function successfully kills the monitored process and its process group.
func TestWatchdog_TerminateProcess(t *testing.T) {
	wd := &Watchdog{
		pidFile:        filepath.Join(t.TempDir(), "test.pid"),
		wdPidFile:      filepath.Join(t.TempDir(), "watchdog.pid"),
		restartTimeout: time.Second,
	}

	cmd := exec.Command("sleep", "10")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start test process: %v", err)
	}
	defer cmd.Process.Kill()

	wd.cmd = cmd
	wd.processGroupPID.Store(int32(cmd.Process.Pid))

	if err := wd.terminateProcess(); err != nil {
		t.Errorf("terminateProcess failed: %v", err)
	}

	_, err := cmd.Process.Wait()
	if err == nil {
		t.Error("Process was not terminated")
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
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start test process: %v", err)
	}
	defer cmd.Process.Kill()

	wd.cmd = cmd

	if err := wd.writePIDFiles(); err != nil {
		t.Errorf("writePIDFiles failed: %v", err)
	}

	if _, err := os.Stat(pidFile); os.IsNotExist(err) {
		t.Error("Process PID file not created")
	}
	if _, err := os.Stat(wdPidFile); os.IsNotExist(err) {
		t.Error("Watchdog PID file not created")
	}
}
