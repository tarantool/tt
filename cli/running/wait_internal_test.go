package running

import (
	"context"
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
	"github.com/tarantool/tt/v3/internal/instances"
	"github.com/tarantool/tt/v3/internal/supervisor"
)

// waitFixture is a watchdog a test plays the part of: it writes the report
// a watchdog would, and closes exited as a watchdog that exits would.
type waitFixture struct {
	watchdog *StartedWatchdog
	rep      *reporter
	exited   chan struct{}
}

func newWaitFixture(t *testing.T, kind instances.Kind,
	restart instances.RestartPolicy,
) *waitFixture {
	t.Helper()

	dir := t.TempDir()
	inst := InstanceCtx{
		AppName: "myapp", InstName: "inst", Kind: kind, RunDir: dir,
		Log: filepath.Join(dir, "tt.log"), Restart: restart,
	}

	report, err := newReportFile(&inst)
	require.NoError(t, err)

	exited := make(chan struct{})

	return &waitFixture{
		watchdog: &StartedWatchdog{Instance: inst, exited: exited, report: report},
		rep:      newReporter(report),
		exited:   exited,
	}
}

// exitWith returns the exit of a process that ended with code.
func exitWith(t *testing.T, code int) supervisor.Exit {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", "-c", "exit "+strconv.Itoa(code))
	err := cmd.Run()

	return supervisor.Exit{Pid: cmd.Process.Pid, State: cmd.ProcessState, Err: err}
}

// TestWaitUpExternal pins the wait for an external process: up once it has
// run for externalGrace.
func TestWaitUpExternal(t *testing.T) {
	fixture := newWaitFixture(t, instances.KindExternal, instances.RestartNo)

	fixture.rep.onEvent(&fixture.watchdog.Instance, supervisor.Started{Pid: 42})

	started := time.Now()
	failures := WaitUp([]*StartedWatchdog{fixture.watchdog}, 10*time.Second, nil)

	require.Len(t, failures, 1)
	require.NoError(t, failures[0])
	assert.GreaterOrEqual(t, time.Since(started), externalGrace)
	assert.NoFileExists(t, fixture.watchdog.report, "the report file is left behind")

	// The watchdog writes no report once the start is done waiting.
	exit := exitWith(t, 1)

	exit.Pid = 42
	fixture.rep.onEvent(&fixture.watchdog.Instance, exit)
	assert.NoFileExists(t, fixture.watchdog.report)
}

// TestWaitUpExited pins that a process that exits before it is up fails the
// start, also when its watchdog starts it again, and that the failure says
// how it exited and quotes the end of tt.log.
func TestWaitUpExited(t *testing.T) {
	for _, restart := range []instances.RestartPolicy{
		instances.RestartNo, instances.RestartOnFailure,
	} {
		t.Run(string(restart), func(t *testing.T) {
			fixture := newWaitFixture(t, instances.KindExternal, restart)
			inst := &fixture.watchdog.Instance

			var log strings.Builder

			for i := range 30 {
				log.WriteString("line " + strconv.Itoa(i) + "\n")
			}

			log.WriteString("E> boom on start\n")
			require.NoError(t, os.WriteFile(inst.Log, []byte(log.String()), 0o600))

			exit := exitWith(t, 3)

			fixture.rep.onEvent(inst, supervisor.Started{Pid: exit.Pid})
			fixture.rep.onEvent(inst, exit)

			failures := WaitUp([]*StartedWatchdog{fixture.watchdog}, 10*time.Second, nil)
			require.ErrorIs(t, failures[0], errNotUp)

			message := failures[0].Error()
			assert.Contains(t, message, "instance myapp:inst did not start: it exited with code 3")
			assert.Contains(t, message, "The last lines of "+inst.Log)
			assert.Contains(t, message, "    E> boom on start")
			assert.NotContains(t, message, "    line 1\n", "more of the log than its end")

			if restart == instances.RestartOnFailure {
				assert.Contains(t, message, "its watchdog starts it again")
			} else {
				assert.NotContains(t, message, "starts it again")
			}
		})
	}
}

// TestWaitUpWatchdogEnds pins that a watchdog that ends without an instance
// up fails the start: one that says why, and one that exits saying nothing.
func TestWaitUpWatchdogEnds(t *testing.T) {
	t.Run("with a reason", func(t *testing.T) {
		fixture := newWaitFixture(t, instances.KindCluster, instances.RestartNo)

		ReportEnd(fixture.watchdog.report, errNotAnswering)

		failures := WaitUp([]*StartedWatchdog{fixture.watchdog}, 10*time.Second, nil)
		require.ErrorIs(t, failures[0], errNotUp)
		require.ErrorContains(t, failures[0], "its watchdog ended: not answering")
	})

	t.Run("silently", func(t *testing.T) {
		fixture := newWaitFixture(t, instances.KindCluster, instances.RestartNo)

		close(fixture.exited)

		failures := WaitUp([]*StartedWatchdog{fixture.watchdog}, 10*time.Second, nil)
		require.ErrorIs(t, failures[0], errNotUp)
		require.ErrorContains(t, failures[0], "its watchdog exited")
	})
}

// TestWaitUpTimeout pins that an instance not up in time fails the start,
// and that the instances are waited for together: two of them take one
// timeout, not two.
func TestWaitUpTimeout(t *testing.T) {
	first := newWaitFixture(t, instances.KindCluster, instances.RestartNo)
	second := newWaitFixture(t, instances.KindCluster, instances.RestartNo)

	// The first runs, with no console socket that answers; the second never
	// starts.
	first.watchdog.Instance.ConsoleSocket = filepath.Join(t.TempDir(), "missing.control")
	first.rep.onEvent(&first.watchdog.Instance, supervisor.Started{Pid: 42})

	const timeout = 500 * time.Millisecond

	started := time.Now()
	failures := WaitUp([]*StartedWatchdog{first.watchdog, second.watchdog}, timeout,
		func(context.Context, *InstanceCtx) (bool, string, error) {
			return false, "", errNotAnswering
		})

	assert.Less(t, time.Since(started), 2*timeout, "the instances were waited for in turn")

	require.ErrorIs(t, failures[0], errNotUpInTime)
	require.ErrorContains(t, failures[0], "is not up in time: it runs, not answering")
	require.ErrorContains(t, failures[0], "after 500ms; it is left to its watchdog")
	require.ErrorIs(t, failures[1], errNotUpInTime)
	require.ErrorContains(t, failures[1], "the instance has not started")
}

// TestWaitUpNotWaited pins that a watchdog started with no report file is not
// waited for.
func TestWaitUpNotWaited(t *testing.T) {
	failures := WaitUp([]*StartedWatchdog{{Instance: InstanceCtx{}, exited: nil, report: ""}},
		time.Millisecond, nil)
	require.NoError(t, failures[0])
}

// TestWaitUpProbes pins that a Tarantool instance is up once the prober says
// so, and that what the prober says of it last tells how far it got.
func TestWaitUpProbes(t *testing.T) {
	fixture := newWaitFixture(t, instances.KindCluster, instances.RestartNo)
	inst := &fixture.watchdog.Instance

	inst.ConsoleSocket = filepath.Join(t.TempDir(), "tarantool.control")
	fixture.rep.onEvent(inst, supervisor.Started{Pid: 42})

	probes := 0
	probe := func(_ context.Context, asked *InstanceCtx) (bool, string, error) {
		assert.Equal(t, inst.ConsoleSocket, asked.ConsoleSocket)

		probes++

		switch probes {
		case 1:
			return false, "", errNotAnswering
		case 2:
			return false, "CONFIG startup_in_progress, BOX loading", nil
		}

		return true, "CONFIG ready, BOX running", nil
	}

	failures := WaitUp([]*StartedWatchdog{fixture.watchdog}, 10*time.Second, probe)
	require.NoError(t, failures[0])
	assert.Equal(t, 3, probes)

	second := newWaitFixture(t, instances.KindCluster, instances.RestartNo)

	second.watchdog.Instance.ConsoleSocket = inst.ConsoleSocket
	second.rep.onEvent(&second.watchdog.Instance, supervisor.Started{Pid: 42})

	failures = WaitUp([]*StartedWatchdog{second.watchdog}, 300*time.Millisecond,
		func(context.Context, *InstanceCtx) (bool, string, error) {
			return false, "CONFIG startup_in_progress, BOX loading", nil
		})
	require.ErrorIs(t, failures[0], errNotUpInTime)
	require.ErrorContains(t, failures[0], "it runs, CONFIG startup_in_progress, BOX loading")
}

// errNotAnswering is the failure of a prober whose instance does not answer.
var errNotAnswering = errors.New("not answering")

// TestDescribeExit pins how the exit of a process is told.
func TestDescribeExit(t *testing.T) {
	assert.Equal(t, "it exited with code 7", describeExit(exitWith(t, 7)))

	cmd := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, cmd.Start())
	require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))

	err := cmd.Wait()

	assert.Equal(t, "it was killed by signal 9 (killed)",
		describeExit(supervisor.Exit{Pid: cmd.Process.Pid, State: cmd.ProcessState, Err: err}))
}

// TestLogTail pins the end of tt.log a failure quotes: the last lines,
// indented; nothing of a log that is not there or is empty.
func TestLogTail(t *testing.T) {
	dir := t.TempDir()

	assert.Empty(t, logTail(filepath.Join(dir, "missing.log"), 3))

	empty := filepath.Join(dir, "empty.log")
	require.NoError(t, os.WriteFile(empty, nil, 0o600))
	assert.Empty(t, logTail(empty, 3))

	short := filepath.Join(dir, "short.log")
	require.NoError(t, os.WriteFile(short, []byte("a\nb\nc\nd\n"), 0o600))
	assert.Equal(t, "    b\n    c\n    d", logTail(short, 3))

	// A long log is read from its end, and its first line read, which may
	// be cut, is dropped.
	long := filepath.Join(dir, "long.log")
	line := strings.Repeat("x", 1000) + "\n"
	require.NoError(t, os.WriteFile(long, []byte(strings.Repeat(line, 200)+"last\n"), 0o600))
	assert.Equal(t, "    "+strings.TrimSuffix(line, "\n")+"\n    last", logTail(long, 2))
}
