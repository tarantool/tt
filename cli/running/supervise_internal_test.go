package running

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
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
func TestWatchdogIntegrityHardStop(t *testing.T) {
	cmdCtx := &cmdcontext.CmdCtx{}

	cmdCtx.Cli.IntegrityCheckPeriod = 1
	cmdCtx.Integrity = integrity.IntegrityCtx{Repository: &tamperedRepository{}}

	test := newWatchdogTest(t, instances.RestartAlways, cmdCtx)
	done := test.run()

	test.waitStarted(1)
	waitSockets(t, &test.inst)

	err := waitDone(t, done)
	require.ErrorIs(t, err, errTampered)

	var runErr *supervisor.Error

	require.ErrorAs(t, err, &runErr)
	assert.Equal(t, supervisor.OpCheck, runErr.Op)
	assert.Len(t, test.children(), 1, "tarantool started again after a failed check")
	assert.Contains(t, test.log.String(), "(ERROR): periodic integrity check failed:")
	assert.Contains(t, test.log.String(), "is not restarted")
	assert.NoFileExists(t, test.inst.PIDFile)
	assertNoSockets(t, &test.inst)
}

// TestWatchdogCleanupAfterRunDirChange pins that the sockets removed after
// tarantool exits are the ones it ran with, when the configuration has moved
// them meanwhile: at the end of the watchdog when the instance is not
// restarted, and before the next start when it is.
func TestWatchdogCleanupAfterRunDirChange(t *testing.T) {
	cases := map[string]instances.RestartPolicy{
		"not restarted": instances.RestartNo, "restarted": instances.RestartAlways,
	}

	for name, policy := range cases {
		t.Run(name, func(t *testing.T) {
			test := newWatchdogTest(t, instances.RestartAlways, &cmdcontext.CmdCtx{})
			done := test.run()

			first := test.waitStarted(1)
			old := test.inst

			waitSockets(t, &old)

			moved := test.moveRunDir()

			test.setRestart(policy)

			// Killed, tarantool leaves its sockets behind.
			require.NoError(t, syscall.Kill(first, syscall.SIGKILL))

			if policy == instances.RestartAlways {
				test.waitStarted(2)
				waitSockets(t, &moved)
				assertNoSockets(t, &old)

				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
			}

			require.NoError(t, waitDone(t, done))
			assertNoSockets(t, &old)
			assertNoSockets(t, &moved)
		})
	}
}

// TestWatchdogCleanupAfterFailedStart pins that the sockets removed at the
// end are those of the tarantool that ran, when the context of the next
// start is ready but that start fails: the paths of the context that never
// ran, where something else may live, are left alone.
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

	moved := test.moveRunDir()

	for _, path := range []string{moved.ConsoleSocket, moved.BinaryPort} {
		require.NoError(t, os.WriteFile(path, nil, 0o600))
	}

	// Killed, tarantool leaves its sockets behind.
	require.NoError(t, syscall.Kill(first, syscall.SIGKILL))

	err := waitDone(t, done)
	require.ErrorIs(t, err, errStart)

	var runErr *supervisor.Error

	require.ErrorAs(t, err, &runErr)
	assert.Equal(t, supervisor.OpStart, runErr.Op)
	assertNoSockets(t, &old)
	assert.FileExists(t, moved.ConsoleSocket)
	assert.FileExists(t, moved.BinaryPort)
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
