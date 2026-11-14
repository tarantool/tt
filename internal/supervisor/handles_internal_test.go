//go:build linux

package supervisor

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startSleep starts a sleep, a program that sets up no signal handling, with
// the shell commands before it, and kills it at the end of the test.
func startSleep(t *testing.T, before string) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", before+"exec sleep 30")
	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// The shell has to have become the sleep.
	require.Eventually(t, func() bool {
		comm, err := os.ReadFile("/proc/" + strconv.Itoa(cmd.Process.Pid) + "/comm")

		return err == nil && strings.TrimSpace(string(comm)) == "sleep"
	}, waitTimeout, pollInterval)

	return cmd
}

// TestHandlesSignal pins what counts as handling a signal: catching or
// ignoring it, and that a process that does neither does not, while a
// process that cannot be asked counts as handling it.
func TestHandlesSignal(t *testing.T) {
	// The Go runtime catches every signal.
	assert.True(t, handlesSignal(os.Getpid(), syscall.SIGHUP))

	plain := startSleep(t, "")
	assert.False(t, handlesSignal(plain.Process.Pid, syscall.SIGHUP))
	assert.False(t, handlesSignal(plain.Process.Pid, syscall.SIGUSR1))

	// An ignored signal stays ignored across exec.
	ignoring := startSleep(t, `trap "" HUP; `)
	assert.True(t, handlesSignal(ignoring.Process.Pid, syscall.SIGHUP))
	assert.False(t, handlesSignal(ignoring.Process.Pid, syscall.SIGUSR1))

	assert.True(t, handlesSignal(deadPid(t), syscall.SIGHUP), "a process that is gone")
	assert.True(t, handlesSignal(plain.Process.Pid, 0), "a signal out of the masks")
	assert.True(t, handlesSignal(plain.Process.Pid, 65), "a signal out of the masks")
}

// TestReloadNotHandled pins that a child that does not handle the reload
// signal does not get it, rather than dying of it as a child that has only
// just been started would: the reload hook runs, the event reports the
// signal not sent, and the child runs on until it is stopped.
func TestReloadNotHandled(t *testing.T) {
	spec := Spec{
		Path:                "sleep",
		Args:                []string{"30"},
		StopSignal:          syscall.SIGTERM,
		StopTimeout:         longStopTimeout,
		ForwardReloadSignal: true,
	}

	var reloads atomic.Int32

	sup := newHarness(t, fixedSource(spec, false), Options{
		StopSignals:  stopSignals,
		ReloadSignal: syscall.SIGHUP,
		OnReload: func() error {
			reloads.Add(1)

			return nil
		},
	})
	sup.start()

	started := waitEvent[Started](t, sup.rec, 1)
	sup.send(syscall.SIGHUP)

	reload := waitEvent[SignalReceived](t, sup.rec, 1)
	assert.Equal(t, syscall.SIGHUP, reload.Signal)
	assert.Equal(t, ActionReload, reload.Action)
	require.ErrorIs(t, reload.Err, ErrReloadNotHandled)
	assert.Equal(t, int32(1), reloads.Load())

	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())

	exit := waitEvent[Exit](t, sup.rec, 1)
	assert.Equal(t, started.Pid, exit.Pid)
	assert.Equal(t, syscall.SIGTERM, termSignal(exit.State), "the child died of the reload")
}
