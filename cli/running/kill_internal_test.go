package running

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
	"github.com/tarantool/tt/v3/internal/pidfile"
)

// killRun is the context of an instance whose run artifacts live in a
// temporary directory, created with the sockets present.
func killRun(t *testing.T) *InstanceCtx {
	t.Helper()

	dir := t.TempDir()
	run := &InstanceCtx{
		PIDFile:       filepath.Join(dir, "tt.pid"),
		ConsoleSocket: filepath.Join(dir, "tarantool.control"),
		BinaryPort:    filepath.Join(dir, "tarantool.sock"),
	}

	for _, path := range []string{run.ConsoleSocket, run.BinaryPort} {
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}

	return run
}

// sleeper starts a process that runs until the test ends, in a process group
// of its own when grouped is set.
func sleeper(t *testing.T, grouped bool) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sleep", "60")

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: grouped}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	return cmd
}

// TestKillRemovesWhatWasItsOwn pins that tt kill removes the pid file and the
// sockets of the watchdog it killed.
func TestKillRemovesWhatWasItsOwn(t *testing.T) {
	run := killRun(t)
	watchdog := sleeper(t, true)
	pid := watchdog.Process.Pid

	require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(pid)), 0o600))
	require.NoError(t, Kill(*run))

	_, err := watchdog.Process.Wait()
	require.NoError(t, err)
	assert.NoFileExists(t, run.PIDFile)
	assert.NoFileExists(t, run.ConsoleSocket)
	assert.NoFileExists(t, run.BinaryPort)
}

// TestKillCleanupSparesNewerOwner pins that the cleanup after tt kill leaves
// alone the pid file and the sockets of a watchdog that came up after the
// killed one: one that holds the pid file, and one whose file names a live
// process that is not the killed one.
func TestKillCleanupSparesNewerOwner(t *testing.T) {
	const killed = 1<<31 - 2

	t.Run("holds the lock", func(t *testing.T) {
		run := killRun(t)

		owned, err := pidfile.Acquire(run.PIDFile, os.Getpid())
		require.NoError(t, err)

		defer func() { require.NoError(t, owned.Release()) }()

		cleanupAfterKill(run, killed, 50*time.Millisecond)

		data, err := os.ReadFile(run.PIDFile)
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(os.Getpid()), string(data))
		assert.FileExists(t, run.ConsoleSocket)
		assert.FileExists(t, run.BinaryPort)
	})

	t.Run("names another process", func(t *testing.T) {
		run := killRun(t)
		other := sleeper(t, false).Process.Pid

		require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(other)), 0o600))

		cleanupAfterKill(run, killed, time.Second)

		data, err := os.ReadFile(run.PIDFile)
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(other), string(data))
		assert.FileExists(t, run.ConsoleSocket)
		assert.FileExists(t, run.BinaryPort)
	})

	t.Run("no pid file", func(t *testing.T) {
		run := killRun(t)

		cleanupAfterKill(run, killed, time.Second)

		assert.NoFileExists(t, run.ConsoleSocket)
		assert.NoFileExists(t, run.BinaryPort)
	})
}
