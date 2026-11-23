package running

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// TestWatchdogRestartsAndStops pins the watchdog of an instance with
// restart: always: tarantool that dies, by SIGINT or SIGKILL, starts again,
// and a SIGINT to the watchdog stops it and the watchdog, which then removes
// its pid file.
func TestWatchdogRestartsAndStops(t *testing.T) {
	test := newWatchdogTest(t, instances.RestartAlways, &cmdcontext.CmdCtx{})
	done := test.run()

	first := test.waitStarted(1)
	assert.Equal(t, os.Getpid(), readPidFile(t, test.inst.PIDFile),
		"the pid file names the watchdog")

	require.NoError(t, syscall.Kill(first, syscall.SIGINT))

	second := test.waitStarted(2)
	require.NoError(t, syscall.Kill(second, syscall.SIGKILL))
	test.waitStarted(3)

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	require.NoError(t, waitDone(t, done))

	assert.NoFileExists(t, test.inst.PIDFile)
	assert.Contains(t, test.log.String(), "(INFO): interrupt received.")
	assert.Contains(t, test.log.String(), "(INFO): waiting for restart timeout 100ms.")
	assert.Contains(t, test.log.String(), "(INFO): the Instance has shutdown.")
}

// TestWatchdogNotRestartable pins that tarantool that dies is not started
// again when its record says restart: no.
func TestWatchdogNotRestartable(t *testing.T) {
	test := newWatchdogTest(t, instances.RestartNo, &cmdcontext.CmdCtx{})
	done := test.run()

	first := test.waitStarted(1)
	require.NoError(t, syscall.Kill(first, syscall.SIGKILL))
	require.NoError(t, waitDone(t, done))

	assert.Len(t, test.children(), 1)
	assert.NoFileExists(t, test.inst.PIDFile)
	assert.Contains(t, test.log.String(), "(INFO): the Instance has shutdown.")
}

// TestWatchdogIntegrityHardStop pins that a failed integrity check stops the
// instance for good, although its record says restart: always, and
// that the watchdog removes the sockets tarantool leaves when it is killed.
// The state of the watchdog has every check, the failed one too, until the
// watchdog ends and removes it; the record of the last exit keeps the
// failure.
func TestWatchdogIntegrityHardStop(t *testing.T) {
	repo := &tamperedRepository{}
	cmdCtx := &cmdcontext.CmdCtx{}

	cmdCtx.Cli.IntegrityCheckPeriod = 1
	cmdCtx.Integrity = integrity.IntegrityCtx{Repository: repo}

	var (
		exits *exitRecorder
		// failed is the state the watchdog published as it killed tarantool
		// for the failed check.
		failed *WatchdogState
	)

	record := func(opts *supervisor.Options) {
		onEvent := opts.OnEvent

		opts.OnEvent = func(event supervisor.Event) {
			onEvent(event)
			exits.onEvent(event)

			killed, ok := event.(supervisor.Killed)
			if ok && killed.Reason == supervisor.KillCheckFailed {
				failed, _ = ReadWatchdogState(exits.inst, os.Getpid())
			}
		}
	}

	test := newWatchdogTest(t, instances.RestartAlways, cmdCtx, record)

	exits = newExitRecorder(&test.inst)

	done := test.run()

	// The files change once tarantool runs with its sockets open. A check
	// failing sooner kills a tarantool that has not started yet, as the
	// first one does on a slow machine, where the start outlasts the
	// period.
	first := test.waitStarted(1)
	waitSockets(t, &test.inst)

	require.Eventually(t, func() bool {
		state, err := ReadWatchdogState(&test.inst, os.Getpid())

		return err == nil && state.IntegrityCheck != nil
	}, 20*time.Second, 10*time.Millisecond, "no integrity check was published")

	passed, err := ReadWatchdogState(&test.inst, os.Getpid())
	require.NoError(t, err)
	assert.True(t, passed.IntegrityCheck.OK)
	require.NotNil(t, passed.Process)
	assert.Equal(t, first, passed.Process.PID)

	repo.tamper()

	err = waitDone(t, done)
	require.ErrorIs(t, err, errTampered)

	var runErr *supervisor.Error

	require.ErrorAs(t, err, &runErr)
	assert.Equal(t, supervisor.OpCheck, runErr.Op)
	assert.Len(t, test.children(), 1, "tarantool started again after a failed check")
	assert.Contains(t, test.log.String(), "(ERROR): periodic integrity check failed:")
	assert.Contains(t, test.log.String(), "is not restarted")
	assert.NoFileExists(t, test.inst.PIDFile)
	assertNoSockets(t, &test.inst)

	require.NotNil(t, failed, "the failed check was not published")
	require.NotNil(t, failed.IntegrityCheck)
	assert.False(t, failed.IntegrityCheck.OK)
	assert.Equal(t, errTampered.Error(), failed.IntegrityCheck.Error)
	assert.NoFileExists(t, filepath.Join(test.inst.RunDir, watchdogStateFileName))

	exits.end(err)

	last, ok := ReadLastExit(&test.inst)
	require.True(t, ok)
	assert.Contains(t, last.Describe(), "its watchdog ended: the periodic check: "+
		errTampered.Error())
}

// TestWatchdogPublishesItsState pins the state the watchdog publishes in the
// run directory for tt instance status: itself, the tarantool it runs, which
// changes when tarantool is started again after a crash, and the count of
// those restarts. It is gone once the watchdog stops.
func TestWatchdogPublishesItsState(t *testing.T) {
	test := newWatchdogTest(t, instances.RestartAlways, &cmdcontext.CmdCtx{})
	done := test.run()

	// stateRuns waits for the state to name the process pid, and returns it.
	stateRuns := func(pid int) *WatchdogState {
		t.Helper()

		var state *WatchdogState

		require.Eventually(t, func() bool {
			var err error

			state, err = ReadWatchdogState(&test.inst, os.Getpid())

			return err == nil && state.Process != nil && state.Process.PID == pid
		}, 20*time.Second, 10*time.Millisecond, "the state does not name the process %d", pid)

		return state
	}

	first := test.waitStarted(1)
	state := stateRuns(first)

	assert.Equal(t, os.Getpid(), state.Watchdog.PID)
	assert.False(t, state.Watchdog.StartedAt.IsZero())
	assert.False(t, state.Process.StartedAt.Before(state.Watchdog.StartedAt))
	assert.Zero(t, state.Restarts)
	assert.Nil(t, state.IntegrityCheck, "no checks without them")

	require.NoError(t, syscall.Kill(first, syscall.SIGKILL))

	second := test.waitStarted(2)

	state = stateRuns(second)
	assert.NotEqual(t, first, second)
	assert.Equal(t, 1, state.Restarts)

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	require.NoError(t, waitDone(t, done))
	assert.NoFileExists(t, filepath.Join(test.inst.RunDir, watchdogStateFileName))
}

// TestWatchdogRestartsItsLaunch pins that the watchdog starts the instance
// from its launch every time: a restart runs the process the first start
// ran, whatever the context of the instance says by then. While it runs,
// the launch is in the run directory, naming the watchdog, for
// tt instance status --details; it goes with the sockets at the end.
func TestWatchdogRestartsItsLaunch(t *testing.T) {
	test := newWatchdogTest(t, instances.RestartAlways, &cmdcontext.CmdCtx{})
	done := test.run()

	first := test.waitStarted(1)
	waitSockets(t, &test.inst)

	running := RunningLaunch(&test.inst)
	require.NotNil(t, running, "the launch is not in the run directory")
	assert.Equal(t, os.Getpid(), running.Watchdog)
	assert.Equal(t, test.src.launch.Spec, running.Spec)
	assert.Equal(t, instances.RestartAlways, running.Instance.Restart)

	firstSpec, err := test.src.Next(t.Context())
	require.NoError(t, err)

	// An edit of the description after the start does not reach the
	// watchdog: it does not read it.
	test.inst.Restart = instances.RestartNo
	test.inst.Env = map[string]string{"EDITED": "1"}

	// Killed, tarantool leaves its sockets behind, and is started again.
	require.NoError(t, syscall.Kill(first, syscall.SIGKILL))
	test.waitStarted(2)

	secondSpec, err := test.src.Next(t.Context())
	require.NoError(t, err)
	assert.Equal(t, firstSpec.Path, secondSpec.Path)
	assert.Equal(t, firstSpec.Args, secondSpec.Args)
	assert.Equal(t, firstSpec.Env, secondSpec.Env)
	assert.NotContains(t, secondSpec.Env, "EDITED=1")

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	require.NoError(t, waitDone(t, done))
	assertNoSockets(t, &test.inst)
	assert.NoFileExists(t, filepath.Join(test.inst.RunDir, launchFileName))
	assert.Nil(t, RunningLaunch(&test.inst))
}

// TestWatchdogRemovesThePidFileOfADeadTarantool pins that the pid file
// tarantool writes where tt tells it to goes once tarantool has exited
// without removing it, killed here: with it, tt instance stop would leave
// it behind. A file that names another process by then is left alone.
func TestWatchdogRemovesThePidFileOfADeadTarantool(t *testing.T) {
	test := newWatchdogTest(t, instances.RestartNo, &cmdcontext.CmdCtx{})
	pidFile := filepath.Join(test.inst.RunDir, layout.TarantoolPidFileName)
	done := test.run()

	first := test.waitStarted(1)

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile)

		return err == nil && strings.TrimSpace(string(data)) == strconv.Itoa(first)
	}, 20*time.Second, 10*time.Millisecond, "tarantool did not write its pid file")

	require.NoError(t, syscall.Kill(first, syscall.SIGKILL))
	require.NoError(t, waitDone(t, done))
	assert.NoFileExists(t, pidFile)

	// The file of a newer process at the same path stays.
	other := os.Getpid()
	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(other)), 0o600))
	test.src.exited(first)
	assert.Equal(t, other, readPidFile(t, pidFile))
}

// TestWatchdogCleanupAfterFailedStart pins that the sockets of the tarantool
// that ran are removed when the start after it fails.
func TestWatchdogCleanupAfterFailedStart(t *testing.T) {
	errStart := errors.New("the start failed")
	starts := 0

	failSecondStart := func(opts *supervisor.Options) {
		opts.Start = func(cmd *exec.Cmd) error {
			starts++
			if starts > 1 {
				return errStart
			}

			return supervisor.StartCmd(cmd)
		}
	}

	test := newWatchdogTest(t, instances.RestartAlways, &cmdcontext.CmdCtx{}, failSecondStart)
	done := test.run()

	first := test.waitStarted(1)
	old := test.inst

	waitSockets(t, &old)

	// Killed, tarantool leaves its sockets behind.
	require.NoError(t, syscall.Kill(first, syscall.SIGKILL))

	err := waitDone(t, done)
	require.ErrorIs(t, err, errStart)

	var runErr *supervisor.Error

	require.ErrorAs(t, err, &runErr)
	assert.Equal(t, supervisor.OpStart, runErr.Op)
	assertNoSockets(t, &old)
}

// TestWatchdogForwardsStopSignal pins that a stop signal reaches tarantool as
// it was sent: SIGQUIT stays SIGQUIT, which tt instance kill --dump relies
// on.
func TestWatchdogForwardsStopSignal(t *testing.T) {
	// Without a core file for the SIGQUIT tarantool dies of.
	var limit syscall.Rlimit

	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_CORE, &limit))

	noCore := syscall.Rlimit{Cur: 0, Max: limit.Max}

	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_CORE, &noCore))
	t.Cleanup(func() { _ = syscall.Setrlimit(syscall.RLIMIT_CORE, &limit) })

	test := newWatchdogTest(t, instances.RestartAlways, &cmdcontext.CmdCtx{})
	done := test.run()

	test.waitStarted(1)
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGQUIT))
	require.NoError(t, waitDone(t, done))

	exits := test.exitsSoFar()
	require.Len(t, exits, 1)
	require.NotNil(t, exits[0].State)

	status, ok := exits[0].State.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	assert.True(t, status.Signaled(), "tarantool exited with %v", exits[0].State)
	assert.Equal(t, syscall.SIGQUIT, status.Signal())
}

// TestStopOutwaitsWatchdog pins that tt instance stop and tt instance kill
// --dump wait for the watchdog longer than the watchdog waits for tarantool
// before it kills it, so that they see the stop complete rather than give up
// first.
func TestStopOutwaitsWatchdog(t *testing.T) {
	assert.Less(t, instanceStopTimeout, process_utils.TerminationTimeout)
}
