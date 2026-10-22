package process_utils_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/process_utils"
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
	require.NoError(t, owned.Keep())

	status, err := process_utils.ExistsAndRecord(testFile)
	require.NoError(t, err)
	require.True(t, status)

	err = cmd.Process.Kill()
	require.NoError(t, err)
	require.Error(t, cmd.Wait())

	statusInvalid, err := process_utils.ExistsAndRecord(invalid)
	require.False(t, statusInvalid)
	require.NoError(t, err)
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

func TestStopProcessWithin(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	require.NoError(t, cmd.Start())

	done := make(chan struct{})

	go func() {
		_ = cmd.Wait()

		close(done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-done
	})

	pidFile := filepath.Join(t.TempDir(), "process.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600))

	pid, err := process_utils.StopProcessWithin(pidFile, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, cmd.Process.Pid, pid)
	require.NoError(t, process_utils.WaitProcessTermination(pid, 0))
}
