package process_utils_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/internal/pidfile"
)

func Test_ExistsAndRecord(t *testing.T) {
	testFile := "test.pid"
	invalid := "invalid.pid"
	cmd := exec.CommandContext(t.Context(), "sleep", "10")

	t.Cleanup(func() {
		_ = os.Remove(testFile)
	})

	err := cmd.Start()
	require.NoError(t, err)

	owned, err := process_utils.CreatePIDFile(testFile, cmd.Process.Pid)
	require.NoError(t, err)

	status, err := process_utils.ExistsAndRecord(testFile)
	require.NoError(t, err)
	require.True(t, status)

	// Left without its lock, the file names a live process that does not
	// own it: no process runs by it.
	require.NoError(t, owned.Keep())

	status, err = process_utils.ExistsAndRecord(testFile)
	require.NoError(t, err)
	require.False(t, status, "a file nobody holds is taken for a running process")

	err = cmd.Process.Kill()
	require.NoError(t, err)
	require.Error(t, cmd.Wait())

	statusInvalid, err := process_utils.ExistsAndRecord(invalid)
	require.False(t, statusInvalid)
	require.NoError(t, err)
}

// TestOwnerStatus pins that the state of a process by its pid file is told
// by the lock of the file: a live process the file names without holding it
// is not running, and one that holds it is, whatever it is.
func TestOwnerStatus(t *testing.T) {
	dir := t.TempDir()

	assert.Equal(t, process_utils.ProcessStoppedCode,
		process_utils.OwnerStatus(filepath.Join(dir, "missing.pid")).Code)

	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	stale := filepath.Join(dir, "stale.pid")
	require.NoError(t, os.WriteFile(stale, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600))

	state := process_utils.OwnerStatus(stale)
	assert.Equal(t, process_utils.ProcessDeadCode, state.Code)
	assert.Equal(t, cmd.Process.Pid, state.PID)
	require.NoError(t, process_utils.CheckPIDFile(stale))

	held := filepath.Join(dir, "held.pid")

	owned, err := process_utils.CreatePIDFile(held, os.Getpid())
	require.NoError(t, err)

	defer func() { require.NoError(t, owned.Release()) }()

	state = process_utils.OwnerStatus(held)
	assert.Equal(t, process_utils.ProcessRunningCode, state.Code)
	assert.Equal(t, os.Getpid(), state.PID)
	require.ErrorContains(t, process_utils.CheckPIDFile(held), "the process already exists")
}

// A live process cannot terminate while it is this test process. A zero wait
// budget makes timeout coverage independent of machine speed.
func TestWaitProcessTerminationTimeout(t *testing.T) {
	require.ErrorContains(t, process_utils.WaitProcessTermination(os.Getpid(), 0),
		"can't terminate the process")
}

func TestInterruptProcessMissingPID(t *testing.T) {
	_, err := process_utils.InterruptProcess(filepath.Join(t.TempDir(), "missing.pid"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestStopProcessWithin stops a process by the pid file it holds: the lock
// of the file it inherited as it started, as TCM started without a watchdog
// holds its own.
func TestStopProcessWithin(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "process.pid")

	reserved, err := pidfile.Reserve(pidFile)
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), "sleep", "30")

	cmd.ExtraFiles = []*os.File{reserved.Lock()}

	require.NoError(t, cmd.Start())
	require.NoError(t, reserved.SetPid(cmd.Process.Pid))
	require.NoError(t, reserved.Keep())

	done := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-done
	})

	pid, err := process_utils.StopProcessWithin(pidFile, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, cmd.Process.Pid, pid)
	require.NoError(t, process_utils.WaitProcessTermination(pid, 0))
}

// TestStopProcessNotOwned pins that a pid file nobody holds stops nothing,
// though the pid in it names a live process: that process does not own it.
func TestStopProcessNotOwned(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	pidFile := filepath.Join(t.TempDir(), "process.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600))

	_, err := process_utils.StopProcessWithin(pidFile, time.Second)
	require.ErrorContains(t, err, "is not running")
	require.NoError(t, cmd.Process.Signal(syscall.Signal(0)), "the process was stopped")
}
