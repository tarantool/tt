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

// fakeWatchdog starts a process that stands for the watchdog of run, in a
// process group of its own, and holds its pid file as a watchdog does: the
// file names it and is locked for as long as it runs. It holds the lock
// through an inherited descriptor, so the lock goes when it dies. The test
// reaps it.
func fakeWatchdog(t *testing.T, run *InstanceCtx) *exec.Cmd {
	t.Helper()

	file, err := os.OpenFile(run.PIDFile, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), "sleep", "60")

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.ExtraFiles = []*os.File{file}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// The lock belongs to the open file description, which the child
	// shares; once the test closes its descriptor, the child alone holds it.
	require.NoError(t, syscall.Flock(int(file.Fd()), syscall.LOCK_EX))

	_, err = file.WriteAt([]byte(strconv.Itoa(cmd.Process.Pid)), 0)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	return cmd
}

// stillRuns reports whether the child pid of the test runs, rather than being
// gone or a zombie, which a probe with signal 0 still finds.
func stillRuns(pid int) bool {
	var status syscall.WaitStatus

	reaped, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)

	return err == nil && reaped == 0
}

// TestKillRemovesWhatWasItsOwn pins that tt instance kill removes the pid
// file and the sockets of the watchdog it killed.
func TestKillRemovesWhatWasItsOwn(t *testing.T) {
	run := killRun(t)
	watchdog := fakeWatchdog(t, run)

	require.NoError(t, Kill(*run))

	_, err := watchdog.Process.Wait()
	require.NoError(t, err)
	assert.NoFileExists(t, run.PIDFile)
	assert.NoFileExists(t, run.ConsoleSocket)
	assert.NoFileExists(t, run.BinaryPort)
}

// TestKillCleanupSparesNewerOwner pins that the cleanup after
// tt instance kill leaves alone the pid file and the sockets of a watchdog
// that came up after the killed one: one that holds the pid file, and one
// whose file names a live process that is not the killed one.
func TestKillCleanupSparesNewerOwner(t *testing.T) {
	const killed = 1<<31 - 2

	t.Run("holds the lock", func(t *testing.T) {
		run := killRun(t)

		owned, err := pidfile.Acquire(run.PIDFile, os.Getpid())
		require.NoError(t, err)

		defer func() { require.NoError(t, owned.Release()) }()

		cleanupAfterKill(run, killed, 50*time.Millisecond, removeSockets)

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

		cleanupAfterKill(run, killed, time.Second, removeSockets)

		data, err := os.ReadFile(run.PIDFile)
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(other), string(data))
		assert.FileExists(t, run.ConsoleSocket)
		assert.FileExists(t, run.BinaryPort)
	})

	t.Run("no pid file", func(t *testing.T) {
		run := killRun(t)

		cleanupAfterKill(run, killed, time.Second, removeSockets)

		assert.NoFileExists(t, run.ConsoleSocket)
		assert.NoFileExists(t, run.BinaryPort)
		assert.NoFileExists(t, run.PIDFile)
	})
}

// TestKillCleanupHoldsThePidFile pins that the sockets of the killed watchdog
// are removed while the pid file is locked, so that a watchdog starting at
// that moment cannot own the instance and have its fresh sockets removed:
// both with the pid file of the killed watchdog and with none at all.
func TestKillCleanupHoldsThePidFile(t *testing.T) {
	const killed = 1<<31 - 2

	cases := map[string]bool{"the file of the killed watchdog": true, "no pid file": false}

	for name, withFile := range cases {
		t.Run(name, func(t *testing.T) {
			run := killRun(t)

			if withFile {
				require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(killed)), 0o600))
			}

			removals := 0

			cleanupAfterKill(run, killed, time.Second, func(run *InstanceCtx) {
				removals++

				// A watchdog starting now.
				_, err := pidfile.Acquire(run.PIDFile, os.Getpid())
				require.ErrorIs(t, err, pidfile.ErrBusy, "a new watchdog owns the instance")

				removeSockets(run)
			})

			assert.Equal(t, 1, removals)
			assert.NoFileExists(t, run.PIDFile)
			assert.NoFileExists(t, run.ConsoleSocket)
			assert.NoFileExists(t, run.BinaryPort)
		})
	}
}

// TestKillStale pins that tt instance kill signals nothing for an instance
// whose pid file nobody holds, whatever the pid in it names: the instance is
// not running, which is no failure, and the file and the sockets the dead
// watchdog left go. A missing pid file is not running either.
func TestKillStale(t *testing.T) {
	gone := 1<<31 - 2

	t.Run("names a process that is gone", func(t *testing.T) {
		run := killRun(t)
		require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(gone)), 0o600))

		require.NoError(t, Kill(*run))
		assert.NoFileExists(t, run.PIDFile)
		assert.NoFileExists(t, run.ConsoleSocket)
		assert.NoFileExists(t, run.BinaryPort)
	})

	t.Run("names a live process", func(t *testing.T) {
		// The pid of the dead watchdog reused by an unrelated process that
		// leads its group, as a watchdog does.
		run := killRun(t)
		other := sleeper(t, true)
		require.NoError(t, os.WriteFile(run.PIDFile,
			[]byte(strconv.Itoa(other.Process.Pid)), 0o600))

		require.NoError(t, Kill(*run))
		assert.True(t, stillRuns(other.Process.Pid), "the process got killed")
		assert.NoFileExists(t, run.PIDFile)
		assert.NoFileExists(t, run.ConsoleSocket)
	})

	t.Run("missing", func(t *testing.T) {
		run := killRun(t)

		require.NoError(t, Kill(*run))
		assert.NoFileExists(t, run.PIDFile)
	})

	t.Run("the pid file cannot be read", func(t *testing.T) {
		run := killRun(t)
		require.NoError(t, os.Mkdir(run.PIDFile, 0o700))

		require.ErrorContains(t, Kill(*run), "failed to kill the processes")
	})
}

// TestQuitStale pins that tt instance kill --dump refuses an instance whose
// pid file nobody holds, signalling nothing: it is not running.
func TestQuitStale(t *testing.T) {
	run := killRun(t)

	run.AppName, run.InstName = "app", "inst"

	other := sleeper(t, true)
	require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(other.Process.Pid)), 0o600))

	err := Quit(*run)
	require.ErrorIs(t, err, errTheInstanceIsNotRunningItMustBeStarted)
	require.ErrorContains(t, err, "app:inst")
	assert.True(t, stillRuns(other.Process.Pid), "the process got a signal")

	require.NoError(t, os.Remove(run.PIDFile))
	require.ErrorIs(t, Quit(*run), errTheInstanceIsNotRunningItMustBeStarted)
}
