package binary

import (
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// interruptSelf runs WatchInterrupts with keep while sleep runs through
// ExecuteCommand, sends tt SIGTERM, and returns the signal the exit got and
// the error of the command. returned is what the caller of the command does
// once it returns, as a deferred cleanup of an installation; it is told
// whether the exit has returned by then.
func interruptSelf(t *testing.T, keep bool, returned func(exited bool)) (os.Signal, error) {
	t.Helper()

	exited := make(chan os.Signal, 1)
	exit := exitOnInterrupt

	var exitReturned atomic.Bool

	exitOnInterrupt = func(sig os.Signal) {
		exited <- sig

		exitReturned.Store(true)
	}

	t.Cleanup(func() { exitOnInterrupt = exit })

	stop := WatchInterrupts(keep)
	defer stop()

	commandErr := make(chan error, 1)

	go func() {
		err := ExecuteCommand("sleep", false, io.Discard, "", "30")

		returned(exitReturned.Load())

		commandErr <- err
	}()

	// Let sleep start before tt is interrupted.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))

	var sig os.Signal

	select {
	case sig = <-exited:
	case <-time.After(15 * time.Second):
		require.FailNow(t, "the interrupt did not end tt")
	}

	select {
	case err := <-commandErr:
		return sig, err
	case <-time.After(time.Second):
		require.FailNow(t, "the command still runs after the interrupt")
	}

	return sig, nil
}

func TestWatchInterruptsRemovesTemporaryFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tarantool_install")
	require.NoError(t, os.MkdirAll(filepath.Join(directory, "build"), 0o755))

	logFile := filepath.Join(t.TempDir(), "tarantool_install.log")
	require.NoError(t, os.WriteFile(logFile, nil, 0o644))

	handedOver := filepath.Join(t.TempDir(), "installed")
	require.NoError(t, os.Mkdir(handedOver, 0o755))

	RemoveOnInterrupt(directory)
	RemoveOnInterrupt(logFile)

	forget := RemoveOnInterrupt(handedOver)
	forget()

	sig, err := interruptSelf(t, false, func(bool) {})
	assert.Equal(t, syscall.SIGTERM, sig)
	require.Error(t, err, "the interrupt stops the command")
	assert.NoDirExists(t, directory)
	assert.NoFileExists(t, logFile)
	assert.DirExists(t, handedOver)

	interrupts.mu.Lock()
	defer interrupts.mu.Unlock()

	assert.Empty(t, interrupts.paths, "the end of the watch forgets every path")
	assert.Empty(t, interrupts.stops, "no command runs")
}

func TestWatchInterruptsKeep(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tarantool_install")
	require.NoError(t, os.Mkdir(directory, 0o755))
	RemoveOnInterrupt(directory)

	_, err := interruptSelf(t, true, func(bool) {})
	require.Error(t, err)
	assert.DirExists(t, directory, "--no-clean keeps the files")
}

// TestWatchInterruptsHoldsTheStoppedCommand: the command an interrupt stops
// returns to its caller only once the interrupt has ended tt, so that a
// deferred cleanup of the caller that forgets a path, as those of an
// installation do, cannot take it from the interrupt before the interrupt
// removes it, or with --no-clean names it.
func TestWatchInterruptsHoldsTheStoppedCommand(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "tt_install")
	require.NoError(t, os.Mkdir(directory, 0o755))

	forget := RemoveOnInterrupt(directory)

	var exitedFirst bool

	_, err := interruptSelf(t, false, func(exited bool) {
		exitedFirst = exited

		forget()
	})
	require.Error(t, err)
	assert.True(t, exitedFirst, "the stopped command returned before the interrupt ended tt")
	assert.NoDirExists(t, directory)
}

// TestExecuteCommandWithoutWatch: with no watch the commands run as before.
func TestExecuteCommandWithoutWatch(t *testing.T) {
	require.NoError(t, ExecuteCommand("true", false, io.Discard, ""))
	require.Error(t, ExecuteCommand("false", false, io.Discard, ""))
}
