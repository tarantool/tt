package running

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// exitOf runs a shell command and returns its exit as the watchdog sees it.
func exitOf(t *testing.T, script string) supervisor.Exit {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), "sh", "-c", script)
	err := cmd.Run()

	return supervisor.Exit{Pid: cmd.Process.Pid, State: cmd.ProcessState, Err: err}
}

// TestRestartsAfter pins how the restart policy of an instance record, and
// restart_on_failure of tt.yaml without one, decide on a restart.
func TestRestartsAfter(t *testing.T) {
	exits := map[string]supervisor.Exit{
		"success":    exitOf(t, "exit 0"),
		"failure":    exitOf(t, "exit 3"),
		"signal":     exitOf(t, "kill -KILL $$"),
		"not waited": {Pid: 1, State: nil, Err: os.ErrProcessDone},
	}

	cases := []struct {
		name        string
		restart     instances.RestartPolicy
		restartable bool
		want        map[string]bool
	}{
		{
			name: "no", restart: instances.RestartNo, restartable: true,
			want: map[string]bool{
				"success": false, "failure": false, "signal": false, "not waited": false,
			},
		},
		{
			name: "on-failure", restart: instances.RestartOnFailure, restartable: false,
			want: map[string]bool{
				"success": false, "failure": true, "signal": true, "not waited": true,
			},
		},
		{
			name: "always", restart: instances.RestartAlways, restartable: false,
			want: map[string]bool{
				"success": true, "failure": true, "signal": true, "not waited": true,
			},
		},
		{
			name: "tt.yaml restart_on_failure", restart: "", restartable: true,
			want: map[string]bool{
				"success": true, "failure": true, "signal": true, "not waited": true,
			},
		},
		{
			name: "tt.yaml without restart_on_failure", restart: "", restartable: false,
			want: map[string]bool{
				"success": false, "failure": false, "signal": false, "not waited": false,
			},
		},
	}

	for _, policy := range cases {
		t.Run(policy.name, func(t *testing.T) {
			inst := InstanceCtx{Restart: policy.restart, Restartable: policy.restartable}

			for exitName, exit := range exits {
				assert.Equal(t, policy.want[exitName], restartsAfter(&inst, exit), exitName)
			}
		})
	}
}

// TestApplyStopSettings pins that a record without stop settings keeps the
// defaults, the stop signal of the watchdog forwarded and 30s to stop, and
// that stop_signal replaces the forwarding with the signal it names.
func TestApplyStopSettings(t *testing.T) {
	cases := []struct {
		name        string
		signal      syscall.Signal
		timeout     time.Duration
		wantSignal  syscall.Signal
		wantForward bool
		wantTimeout time.Duration
	}{
		{"defaults", 0, 0, syscall.SIGINT, true, instanceStopTimeout},
		{"stop_signal", syscall.SIGTERM, 0, syscall.SIGTERM, false, instanceStopTimeout},
		{"stop_timeout", 0, time.Minute, syscall.SIGINT, true, time.Minute},
		{"both", syscall.SIGUSR1, time.Second, syscall.SIGUSR1, false, time.Second},
	}

	for _, settings := range cases {
		t.Run(settings.name, func(t *testing.T) {
			spec := tarantoolSpec("tarantool", nil, nil, "", specOptions{})
			inst := InstanceCtx{StopSignal: settings.signal, StopTimeout: settings.timeout}

			applyStopSettings(&spec, &inst)

			assert.Equal(t, settings.wantSignal, spec.StopSignal)
			assert.Equal(t, settings.wantForward, spec.ForwardStopSignal)
			assert.Equal(t, settings.wantTimeout, spec.StopTimeout)
		})
	}
}

// TestStopWait pins that tt stop waits for an instance at least as long as
// its record lets it take to stop.
func TestStopWait(t *testing.T) {
	margin := process_utils.TerminationTimeout - instanceStopTimeout

	assert.Equal(t, process_utils.TerminationTimeout, stopWait(&InstanceCtx{}))
	assert.Equal(t, process_utils.TerminationTimeout,
		stopWait(&InstanceCtx{StopTimeout: time.Second}))
	assert.Equal(t, 2*time.Minute+margin, stopWait(&InstanceCtx{StopTimeout: 2 * time.Minute}))
}

// useExitApp makes the instance a script that exits on its own, with the
// status in the exit_status environment variable at its start.
func (test *watchdogTest) useExitApp(status string) {
	test.t.Helper()

	script, err := filepath.Abs(filepath.Join(instTestAppDir, "exit_test_app.lua"))
	require.NoError(test.t, err)

	test.t.Setenv("exit_status", status)

	test.mu.Lock()
	defer test.mu.Unlock()

	test.inst.InstanceScript = script
}

// TestWatchdogRestartPolicy pins the watchdog following the restart policy
// of the instance record, which wins over restart_on_failure.
func TestWatchdogRestartPolicy(t *testing.T) {
	t.Run("on-failure", func(t *testing.T) {
		test := newWatchdogTest(t, false, &cmdcontext.CmdCtx{})
		test.setRestart(instances.RestartOnFailure)
		test.useExitApp("1")

		done := test.run()

		test.waitStarted(1)
		test.waitStarted(2)

		t.Setenv("exit_status", "0")
		require.NoError(t, waitDone(t, done))

		exits := test.exitsSoFar()
		require.GreaterOrEqual(t, len(exits), 2)
		assert.False(t, exits[0].State.Success())
		assert.True(t, exits[len(exits)-1].State.Success(), "a clean exit is not restarted")
		assert.NoFileExists(t, test.inst.PIDFile)
	})

	t.Run("on-failure after a signal", func(t *testing.T) {
		test := newWatchdogTest(t, false, &cmdcontext.CmdCtx{})
		test.setRestart(instances.RestartOnFailure)

		done := test.run()

		first := test.waitStarted(1)
		require.NoError(t, syscall.Kill(first, syscall.SIGKILL))
		test.waitStarted(2)

		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
		require.NoError(t, waitDone(t, done))

		assert.Len(t, test.children(), 2)
	})

	t.Run("always", func(t *testing.T) {
		test := newWatchdogTest(t, false, &cmdcontext.CmdCtx{})
		test.setRestart(instances.RestartAlways)
		test.useExitApp("0")

		done := test.run()

		test.waitStarted(1)
		test.waitStarted(2)

		require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
		require.NoError(t, waitDone(t, done))

		assert.True(t, test.exitsSoFar()[0].State.Success(), "a clean exit is restarted")
	})

	t.Run("no", func(t *testing.T) {
		test := newWatchdogTest(t, true, &cmdcontext.CmdCtx{})
		test.setRestart(instances.RestartNo)

		done := test.run()

		first := test.waitStarted(1)
		require.NoError(t, syscall.Kill(first, syscall.SIGKILL))
		require.NoError(t, waitDone(t, done))

		assert.Len(t, test.children(), 1, "restart: no wins over restart_on_failure")
	})
}
