package running

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/scope"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// endedProcess returns the state of a shell that ran script and ended.
func endedProcess(t *testing.T, script string) *os.ProcessState {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", "-c", script)

	_ = cmd.Run()

	require.NotNil(t, cmd.ProcessState)

	return cmd.ProcessState
}

// TestForegroundExitCodes pins the exit codes of the watchdog of
// tt instance start --foreground, which tell the tt in the foreground how
// the instance ended: a stop in time is 0 whatever the instance exited
// with, a kill on a stop is ExitKilledOnStop, an end of its own is its own
// code, and a failure of the watchdog is 1.
func TestForegroundExitCodes(t *testing.T) {
	exitedZero := endedProcess(t, "exit 0")
	exitedThree := endedProcess(t, "exit 3")
	terminated := endedProcess(t, "kill -TERM $$")
	killedByUser1 := endedProcess(t, "kill -USR1 $$")

	cases := []struct {
		name   string
		events []supervisor.Event
		err    error
		want   int
	}{
		{
			name:   "exited on its own with 0",
			events: []supervisor.Event{supervisor.Exit{State: exitedZero}},
			want:   0,
		},
		{
			name:   "exited on its own with 3",
			events: []supervisor.Event{supervisor.Exit{State: exitedThree}},
			want:   3,
		},
		{
			name:   "killed by a signal on its own",
			events: []supervisor.Event{supervisor.Exit{State: killedByUser1}},
			want:   signalExitBase + int(syscall.SIGUSR1),
		},
		{
			name: "stopped, exited with a failure in time",
			events: []supervisor.Event{
				supervisor.SignalReceived{Signal: syscall.SIGINT, Action: supervisor.ActionStop},
				supervisor.Exit{State: exitedThree},
			},
			want: 0,
		},
		{
			name: "stopped, ended by SIGTERM in time",
			events: []supervisor.Event{
				supervisor.SignalReceived{Signal: syscall.SIGTERM, Action: supervisor.ActionStop},
				supervisor.Exit{State: terminated},
			},
			want: 0,
		},
		{
			name: "stopped, killed after the stop timeout",
			events: []supervisor.Event{
				supervisor.SignalReceived{Signal: syscall.SIGTERM, Action: supervisor.ActionStop},
				supervisor.Killed{Reason: supervisor.KillStopTimeout},
				supervisor.Exit{State: terminated},
			},
			want: ExitKilledOnStop,
		},
		{
			name: "stopped after it exited on its own",
			events: []supervisor.Event{
				supervisor.Exit{State: exitedThree},
				supervisor.SignalReceived{Signal: syscall.SIGINT, Action: supervisor.ActionStop},
			},
			want: 3,
		},
		{
			name: "stopped before it started",
			events: []supervisor.Event{
				supervisor.SignalReceived{Signal: syscall.SIGINT, Action: supervisor.ActionStop},
			},
			want: 0,
		},
		{
			name:   "the exit could not be waited for",
			events: []supervisor.Event{supervisor.Exit{State: nil}},
			want:   watchdogFailure,
		},
		{
			name: "the watchdog failed",
			events: []supervisor.Event{
				supervisor.Killed{Reason: supervisor.KillCheckFailed},
				supervisor.Exit{State: terminated},
			},
			err:  &supervisor.Error{Op: supervisor.OpCheck, Err: assert.AnError},
			want: watchdogFailure,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var end supervisionEnd

			for _, event := range test.events {
				end.record(event)
			}

			end.err = test.err

			assert.Equal(t, test.want, end.code())
		})
	}
}

// foregroundApp is the code of the instance of a foreground test: it says
// hello on its standard output, counts its starts in the file starts_file
// names, and exits with the status exit_status gives, when it gives one.
const foregroundApp = `io.stdout:write('the instance says hello\n')
io.stdout:flush()

local starts = io.open(os.getenv('starts_file'), 'a')
starts:write('start\n')
starts:close()

local status = os.getenv('exit_status')
if status ~= nil and status ~= '' then
    os.exit(tonumber(status))
end
`

// foregroundInstance lays out a project whose instance runs foregroundApp
// and restarts always, and returns its context and the file it counts its
// starts in.
func foregroundInstance(t *testing.T, exitStatus string) (InstanceCtx, string) {
	t.Helper()

	setScopeRoots(t)

	// The project is short enough for the sockets of the instance under its
	// .tarantool, which tarantool binds by the full path then.
	dir := filepath.Join(shortTempDir(t), fixtureApp)
	writeFiles(t, dir, appFiles("{restart: always}", "app:\n  file: app.lua\n"))
	writeFiles(t, dir, map[string]string{"app.lua": foregroundApp})

	fixture := appFixture{scope: app.ScopeProject, root: scope.ProjectRoot(dir), dir: dir, link: ""}
	inst := fixture.instanceCtx(t)
	require.Equal(t, instances.RestartAlways, inst.Restart)

	starts := filepath.Join(dir, "starts")

	t.Setenv("starts_file", starts)
	t.Setenv("exit_status", exitStatus)

	return inst, starts
}

// countStarts returns how many times the instance has started.
func countStarts(t *testing.T, starts string) int {
	t.Helper()

	data, err := os.ReadFile(starts)
	if os.IsNotExist(err) {
		return 0
	}

	require.NoError(t, err)

	return strings.Count(string(data), "start\n")
}

// foregroundRun is StartForeground running in the background of a test.
type foregroundRun struct {
	code int
	err  error
}

// startForeground runs StartForeground for inst, copying the output of the
// instance to echo, and returns the channel its result comes on.
func startForeground(t *testing.T, inst *InstanceCtx, echo *lockedBuffer) <-chan foregroundRun {
	t.Helper()

	done := make(chan foregroundRun, 1)

	go func() {
		code, err := StartForeground(context.Background(), &cmdcontext.CmdCtx{}, inst, echo)
		done <- foregroundRun{code: code, err: err}
	}()

	return done
}

// stopForeground stops the watchdog StartForeground runs as the tt in the
// foreground does, with a stop signal, and returns how it ended.
func stopForeground(t *testing.T, done <-chan foregroundRun) foregroundRun {
	t.Helper()

	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))

	select {
	case result := <-done:
		return result
	case <-time.After(40 * time.Second):
		require.FailNow(t, "the watchdog did not end")
	}

	return foregroundRun{code: 0, err: nil}
}

// TestForegroundExitCode pins that the watchdog of tt instance start
// --foreground does not restart its instance, whatever its restart policy
// says, and ends with its exit code; and that the output of the instance
// goes to tt.log and to the standard output of the watchdog.
func TestForegroundExitCode(t *testing.T) {
	inst, starts := foregroundInstance(t, "3")

	var echo lockedBuffer

	code, err := StartForeground(t.Context(), &cmdcontext.CmdCtx{}, &inst, &echo)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, 1, countStarts(t, starts), "the instance restarted")

	assert.Contains(t, echo.String(), "the instance says hello")
	assert.Contains(t, echo.String(), "I> Tarantool", "tarantool logs to its standard error")

	log, err := os.ReadFile(inst.Log)
	require.NoError(t, err)
	assert.Contains(t, string(log), "the instance says hello")

	assert.NoFileExists(t, inst.PIDFile)
}

// TestForegroundStop pins that a stop signal stops the instance of the
// watchdog of tt instance start --foreground, which then ends with 0; and
// that the pid file of the instance names the watchdog while it runs, so
// that tt instance status and stop reach it.
func TestForegroundStop(t *testing.T) {
	inst, starts := foregroundInstance(t, "")

	done := startForeground(t, &inst, &lockedBuffer{})

	require.Eventually(t, func() bool {
		return countStarts(t, starts) == 1 &&
			Status(&inst).Code == process_utils.ProcessRunningCode
	}, 20*time.Second, 50*time.Millisecond, "the instance did not start")

	assert.Equal(t, os.Getpid(), readPidFile(t, inst.PIDFile), "the pid file names the watchdog")

	result := stopForeground(t, done)
	require.NoError(t, result.err)
	assert.Equal(t, 0, result.code)

	assert.Equal(t, 1, countStarts(t, starts))
	assert.NoFileExists(t, inst.PIDFile)
	assertNoSockets(t, &inst)
}

// TestForegroundExternalStop pins that an external process stopped by a
// stop signal of the watchdog of tt instance start --foreground ends it with
// 0, and one that does not stop in its stop_timeout with ExitKilledOnStop.
func TestForegroundExternalStop(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		extra string
		want  int
	}{
		{name: "stops", mode: processServe, extra: "", want: 0},
		{
			name: "does not stop", mode: processStubborn, extra: ", stop_timeout: 1s",
			want: ExitKilledOnStop,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			setScopeRoots(t)

			_, inst, files := processFixture(t, test.mode, test.extra)

			var echo lockedBuffer

			done := startForeground(t, &inst, &echo)
			run := waitProcess(t, &inst, files)

			result := stopForeground(t, done)
			require.NoError(t, result.err)
			assert.Equal(t, test.want, result.code)
			assert.Equal(t, signalNames(syscall.SIGTERM), signalsOf(files, run.Pid))
			assert.True(t, gone(run.Pid))
			assert.Contains(t, echo.String(), "got terminated", "the output of the process")
		})
	}
}
