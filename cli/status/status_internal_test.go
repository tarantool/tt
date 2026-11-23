package status

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/internal/instances"
)

// These fixtures encode [{uuid: uuid-a, read_only: RW, box_status: running,
// replication_info: [{uuid: uuid-b, upstream: {status: disconnected,
// message: connection refused}}]}] and the healthy uuid-b instance, respectively.
const (
	stateA = "kYSkdXVpZKZ1dWlkLWGpcmVhZF9vbmx5olJXqmJveF9zdGF0dXOncnVubmluZ7ByZXBsaWNhdGlvbl9p" +
		"bmZvkYKkdXVpZKZ1dWlkLWKodXBzdHJlYW2CpnN0YXR1c6xkaXNjb25uZWN0ZWSnbWVzc2FnZbJjb25u" +
		"ZWN0aW9uIHJlZnVzZWQ="
	stateB = "kYOpcmVhZF9vbmx5olJPqmJveF9zdGF0dXOncnVubmluZ6R1dWlkpnV1aWQtYg=="
)

type capturePrinter struct {
	instances instanceStatusMap
}

func (printer *capturePrinter) Print(instances map[string]*instanceStatus) error {
	printer.instances = instances
	return nil
}

func serveConsole(t *testing.T, handler func(net.Conn)) string {
	t.Helper()

	//nolint:usetesting // The listener path must fit sun_path on macOS too.
	root, err := os.MkdirTemp("/tmp", "tt-status-test-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(root) })

	address := filepath.Join(root, "console.sock")

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", address)
	if err != nil {
		t.Fatal(err)
	}

	var (
		server sync.WaitGroup
		mutex  sync.Mutex
	)

	connections := make(map[net.Conn]struct{})
	closing := false

	server.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			mutex.Lock()

			if closing {
				_ = conn.Close()
				mutex.Unlock()

				return
			}

			connections[conn] = struct{}{}
			mutex.Unlock()

			server.Go(func() {
				defer func() {
					_ = conn.Close()

					mutex.Lock()
					delete(connections, conn)
					mutex.Unlock()
				}()

				handler(conn)
			})
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()

		mutex.Lock()

		closing = true

		for conn := range connections {
			_ = conn.Close()
		}

		mutex.Unlock()

		server.Wait()
	})

	return address
}

func greetAndRead(conn net.Conn) error {
	_, err := fmt.Fprint(conn, "Tarantool 3.6.0 (Lua console)\n")
	if err != nil {
		return err
	}

	_, err = bufio.NewReader(conn).ReadString('\n')

	return err
}

func writeState(conn net.Conn, state string) {
	_, _ = fmt.Fprintf(conn, "---\n- data_enc: %s\n...\n", state)
}

func waitFor[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not complete")

		var zero T

		return zero
	}
}

func TestStatusParallelAndClosesConnections(t *testing.T) {
	const count = 4

	arrived := make(chan struct{}, count)
	release := make(chan struct{})
	closed := make(chan struct{}, count)
	address := serveConsole(t, func(conn net.Conn) {
		if greetAndRead(conn) != nil {
			return
		}

		arrived <- struct{}{}

		<-release
		writeState(conn, stateB)

		_, _ = io.Copy(io.Discard, conn)

		closed <- struct{}{}
	})

	t.Cleanup(func() { close(release) })

	instances := make([]running.InstanceCtx, count)
	for i := range instances {
		instances[i] = running.InstanceCtx{AppName: fmt.Sprintf("app%d", i),
			ConsoleSocket: address}
	}

	printer := &capturePrinter{}
	done := make(chan error, 1)

	go func() { done <- Status(running.RunningCtx{Instances: instances}, printer, 0) }()

	for range count {
		waitFor(t, arrived)
	}

	// Every request must reach the server before any is allowed to finish.
	for range count {
		release <- struct{}{}
	}

	err := waitFor(t, done)
	if err != nil {
		t.Fatal(err)
	}

	for range count {
		waitFor(t, closed)
	}

	if len(printer.instances) != count {
		t.Fatalf("got %d statuses, want %d", len(printer.instances), count)
	}
}

func TestStatusResolvesReplicationAfterCollection(t *testing.T) {
	firstDone := make(chan struct{})
	addressA := serveConsole(t, func(conn net.Conn) {
		if greetAndRead(conn) != nil {
			return
		}

		writeState(conn, stateA)

		_, _ = io.Copy(io.Discard, conn)

		close(firstDone)
	})
	addressB := serveConsole(t, func(conn net.Conn) {
		if greetAndRead(conn) != nil {
			return
		}

		select {
		case <-firstDone:
		case <-t.Context().Done():
			return
		}

		writeState(conn, stateB)
	})

	printer := &capturePrinter{}

	err := Status(running.RunningCtx{Instances: []running.InstanceCtx{
		{AppName: "app", InstName: "a", ConsoleSocket: addressA},
		{AppName: "app", InstName: "b", ConsoleSocket: addressB},
	}}, printer, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	got := printer.instances["app:a"]
	if got.Upstream != "disconnected" || len(got.Alerts) != 1 ||
		!strings.Contains(got.Alerts[0].Message, `instance with name "app:b"`) {
		t.Fatalf("replication info was not resolved: %+v", got)
	}
}

func TestStatusTimeout(t *testing.T) {
	for _, stage := range []string{"greeting", "eval", "push"} {
		t.Run(stage, func(t *testing.T) {
			closed := make(chan struct{}, 1)
			address := serveConsole(t, func(conn net.Conn) {
				defer func() { closed <- struct{}{} }()

				if stage != "greeting" && greetAndRead(conn) != nil {
					return
				}

				if stage == "push" {
					ticker := time.NewTicker(5 * time.Millisecond)
					defer ticker.Stop()

					for range ticker.C {
						_, err := io.WriteString(conn,
							"%TAG !push! tag:tarantool.io/push,2018\n--- 1\n...\n")
						if err != nil {
							return
						}
					}
				}

				_, _ = io.Copy(io.Discard, conn)
			})
			done := make(chan statusResult, 1)

			go func() {
				done <- processStatusForInstance(running.InstanceCtx{
					AppName: "app", ConsoleSocket: address,
				}, 50*time.Millisecond)
			}()

			result := waitFor(t, done)
			waitFor(t, closed)

			if stage != "greeting" && (len(result.status.Alerts) != 1 ||
				!strings.Contains(result.status.Alerts[0].Message, "context deadline exceeded")) {
				t.Fatalf("missing timeout alert: %+v", result.status.Alerts)
			}
		})
	}
}

func TestStatusZeroTimeoutWaitsForResponse(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	address := serveConsole(t, func(conn net.Conn) {
		if greetAndRead(conn) != nil {
			return
		}

		close(arrived)
		<-release
		writeState(conn, stateB)
	})
	t.Cleanup(func() { close(release) })

	done := make(chan statusResult, 1)

	go func() {
		done <- processStatusForInstance(running.InstanceCtx{
			AppName: "app", ConsoleSocket: address,
		}, 0)
	}()

	waitFor(t, arrived)

	select {
	case result := <-done:
		t.Fatalf("returned before the server responded: %+v", result)
	default:
	}

	release <- struct{}{}

	result := waitFor(t, done)
	if result.status.Box != "running" {
		t.Fatalf("failed to collect status: %+v", result.status)
	}
}

// recordingPrinter keeps what Status prints.
type recordingPrinter struct {
	printed map[string]*instanceStatus
}

func (printer *recordingPrinter) Print(instances map[string]*instanceStatus) error {
	printer.printed = instances

	return nil
}

// testProcessPID is the pid of the process the watchdog of a running
// instance of a test runs, as its state says (runningInstance). Nothing asks
// whether it runs.
const testProcessPID = 424242

// watchdogStartedAt is when the watchdog of a running instance of a test
// started, a second before its process, as the state of the watchdog says,
// in a time zone of its own: the details show the time as it was recorded.
var watchdogStartedAt = time.Date(2026, 11, 23, 10, 0, 0, 0, time.FixedZone("", 3*60*60))

// publishState writes state as the state the watchdog of inst publishes in
// its run directory.
func publishState(t *testing.T, inst *running.InstanceCtx, state *running.WatchdogState) {
	t.Helper()

	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(inst.RunDir, "watchdog.json"), data, 0o600))
}

// watchdogState is the state the test process publishes as the watchdog of
// an instance that runs testProcessPID, which it has restarted restarts times.
func watchdogState(restarts int) *running.WatchdogState {
	return &running.WatchdogState{
		Version:  1,
		Watchdog: running.StateProcess{PID: os.Getpid(), StartedAt: watchdogStartedAt},
		Process: &running.StateProcess{
			PID: testProcessPID, StartedAt: watchdogStartedAt.Add(time.Second),
		},
		Restarts:       restarts,
		IntegrityCheck: nil,
	}
}

// runningInstance returns the context of the instance inst of myapp of kind,
// whose pid file the test process holds, so that it counts as running, whose
// watchdog, the test process, publishes that it runs testProcessPID, and
// whose console socket does not exist.
func runningInstance(t *testing.T, inst string, kind instances.Kind) running.InstanceCtx {
	t.Helper()

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "tt.pid")

	owned, err := process_utils.CreatePIDFile(pidFile, os.Getpid())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, owned.Release()) })

	ctx := running.InstanceCtx{
		AppName:  "myapp",
		InstName: inst,
		Kind:     kind,
		PIDFile:  pidFile,
		RunDir:   dir,
	}

	if kind != instances.KindExternal {
		ctx.ConsoleSocket = filepath.Join(dir, "tarantool.control")
	}

	publishState(t, &ctx, watchdogState(0))

	return ctx
}

// TestStatusExternal pins that tt instance status shows the state of the
// process of an external process and asks it nothing more: no alert about a
// console it does not have, while a running Tarantool instance without its
// console gets one.
func TestStatusExternal(t *testing.T) {
	printer := &recordingPrinter{}

	err := Status(running.RunningCtx{Instances: []running.InstanceCtx{
		runningInstance(t, "storage-1", instances.KindCluster),
		runningInstance(t, "worker-1", instances.KindExternal),
	}}, printer, DefaultInstanceTimeout)
	require.NoError(t, err)
	require.Len(t, printer.printed, 2)

	storage := printer.printed["myapp:storage-1"]
	require.NotNil(t, storage)
	assert.Equal(t, process_utils.ProcStateRunning.Status, storage.Status)
	assert.Len(t, storage.Alerts, 1, "a Tarantool instance without its console")

	worker := printer.printed["myapp:worker-1"]
	require.NotNil(t, worker)
	assert.Equal(t, process_utils.ProcStateRunning.Status, worker.Status)
	require.NotNil(t, worker.PID)
	assert.Equal(t, testProcessPID, *worker.PID, "the process, not its watchdog")
	require.NotNil(t, worker.WatchdogPID)
	assert.Equal(t, os.Getpid(), *worker.WatchdogPID)
	assert.Empty(t, worker.Alerts)
	assert.Empty(t, worker.Mode)
	assert.Equal(t, defaultModuleStatus, worker.Config)
	assert.Equal(t, defaultModuleStatus, worker.Box)
	assert.Equal(t, defaultModuleStatus, worker.Upstream)
}

// TestStatusProcess pins that the PID of a running instance is the pid of the
// process its watchdog runs, as the watchdog publishes it, in the table, in
// JSON and in YAML alike, with the pid of the watchdog, when the process
// started, the count of its restarts and the last integrity check apart, and
// that the details show them all.
func TestStatusProcess(t *testing.T) {
	inst := runningInstance(t, "storage-1", instances.KindCluster)
	state := watchdogState(2)

	state.IntegrityCheck = &running.IntegrityCheck{
		At: watchdogStartedAt.Add(time.Minute), OK: true, Error: "",
	}
	publishState(t, &inst, state)

	ctx := running.RunningCtx{Instances: []running.InstanceCtx{inst}}
	printer := &recordingPrinter{}

	require.NoError(t, Status(ctx, printer, DefaultInstanceTimeout))

	storage := printer.printed["myapp:storage-1"]
	require.NotNil(t, storage)
	require.NotNil(t, storage.PID)
	assert.Equal(t, testProcessPID, *storage.PID)
	require.NotNil(t, storage.WatchdogPID)
	assert.Equal(t, os.Getpid(), *storage.WatchdogPID)
	require.NotNil(t, storage.StartedAt)
	assert.True(t, watchdogStartedAt.Add(time.Second).Equal(*storage.StartedAt))
	require.NotNil(t, storage.Restarts)
	assert.Equal(t, 2, *storage.Restarts)
	require.NotNil(t, storage.LastIntegrityCheck)
	assert.True(t, storage.LastIntegrityCheck.OK)
	assert.True(t, state.IntegrityCheck.At.Equal(storage.LastIntegrityCheck.At))

	table := captureStdout(t, func() error {
		return Status(ctx, NewTablePrinter(), DefaultInstanceTimeout)
	})
	assert.Regexp(t, `(?m)^\s*myapp:storage-1\s+RUNNING\s+`+strconv.Itoa(testProcessPID)+`\s`,
		table)
	assert.NotContains(t, table, strconv.Itoa(os.Getpid()))

	out := captureStdout(t, func() error {
		return Status(ctx, NewJSONPrinter(), DefaultInstanceTimeout)
	})

	var decoded map[string]map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &decoded))

	fields := decoded["myapp:storage-1"]
	assert.InDelta(t, testProcessPID, fields["pid"], 0)
	assert.InDelta(t, os.Getpid(), fields["watchdog_pid"], 0)
	assert.Equal(t, watchdogStartedAt.Add(time.Second).Format(time.RFC3339Nano),
		fields["started_at"])
	assert.InDelta(t, 2, fields["restarts"], 0)
	assert.Equal(t, map[string]any{
		"at": watchdogStartedAt.Add(time.Minute).Format(time.RFC3339Nano), "ok": true,
	}, fields["last_integrity_check"])

	out = captureStdout(t, func() error {
		return Status(ctx, NewYAMLPrinter(), DefaultInstanceTimeout)
	})
	assert.Contains(t, out, "\n    pid: "+strconv.Itoa(testProcessPID)+"\n")
	assert.Contains(t, out, "\n    watchdog_pid: "+strconv.Itoa(os.Getpid())+"\n")
	assert.Contains(t, out, "\n    restarts: 2\n")
	assert.Contains(t, out, "\n    last_integrity_check:\n        at: ")

	table = captureStdout(t, func() error {
		return Status(ctx, NewTablePrinter(WithDetails(true)), DefaultInstanceTimeout)
	})
	assert.Contains(t, table, fmt.Sprintf("Watchdog of myapp:storage-1: PID %d\n", os.Getpid()))
	assert.Contains(t, table, fmt.Sprintf("Process of myapp:storage-1: PID %d, started at "+
		"2026-11-23 10:00:01\n", testProcessPID))
	assert.Contains(t, table, "Restarts of myapp:storage-1: 2\n")
	assert.Contains(t, table,
		"Last integrity check of myapp:storage-1: passed, at 2026-11-23 10:01:00\n")

	// A failed check, which kills the process and ends the watchdog, shows
	// as long as the watchdog has not ended yet.
	state.Process = nil
	state.IntegrityCheck = &running.IntegrityCheck{
		At: watchdogStartedAt.Add(time.Minute), OK: false, Error: "app.lua changed",
	}
	publishState(t, &inst, state)

	table = captureStdout(t, func() error {
		return Status(ctx, NewTablePrinter(WithDetails(true)), DefaultInstanceTimeout)
	})
	assert.Contains(t, table, "Process of myapp:storage-1: none runs now")
	assert.Contains(t, table, "Last integrity check of myapp:storage-1: failed, "+
		"at 2026-11-23 10:01:00: app.lua changed\n")
	assert.Regexp(t, `(?m)^\s*myapp:storage-1\s+RUNNING\s+--\s`, table,
		"the PID of no process is empty, the columns after it stay")

	out = captureStdout(t, func() error {
		return Status(ctx, NewJSONPrinter(), DefaultInstanceTimeout)
	})
	require.NoError(t, json.Unmarshal([]byte(out), &decoded))

	fields = decoded["myapp:storage-1"]
	assert.Contains(t, fields, "pid")
	assert.Nil(t, fields["pid"])
	assert.InDelta(t, os.Getpid(), fields["watchdog_pid"], 0)
	assert.NotContains(t, fields, "started_at")
	assert.InDelta(t, 2, fields["restarts"], 0)
}

// TestStatusUnknownProcess pins that the PID of a running instance is not
// known when its watchdog publishes no state that names it: a watchdog that
// has only just taken the pid file has published none yet, a watchdog an
// older tt started publishes none, one that died left the state there, or the
// state is of another format. The pid of the watchdog is known all the same,
// and the details say why the PID is not, and how to fix it: a missing state
// is not blamed on an older tt alone, since a watchdog that is starting looks
// the same for a moment.
func TestStatusUnknownProcess(t *testing.T) {
	stale := watchdogState(0)

	stale.Watchdog.PID = os.Getpid() + 1

	staleData, err := json.Marshal(stale)
	require.NoError(t, err)

	for _, test := range []struct {
		name string
		// state is the state file, none when it is nil.
		state []byte
		unit  string
		note  string
	}{
		{
			name:  "no state",
			state: nil,
			unit:  "",
			note: "the PID of its process is not known, as the watchdog has published no " +
				"state: it has only just started, or an older tt started it, and then " +
				"tt instance restart myapp:storage-1 fixes that",
		},
		{
			name:  "the state of a dead watchdog",
			state: staleData,
			unit:  "",
			note:  "as the watchdog has published no state",
		},
		{
			name:  "a unit",
			state: nil,
			unit:  "myapp@storage-1.service",
			note:  "systemctl restart myapp@storage-1.service fixes that",
		},
		{
			name:  "another format",
			state: []byte(`{"version":2}`),
			unit:  "",
			note: "the state of the watchdog is of another format: version 2, not 1, " +
				"so the PID of its process is not known: " +
				"tt instance restart myapp:storage-1 fixes that",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			inst := runningInstance(t, "storage-1", instances.KindCluster)

			inst.Unit = test.unit

			stateFile := filepath.Join(inst.RunDir, "watchdog.json")

			require.NoError(t, os.Remove(stateFile))

			if test.state != nil {
				require.NoError(t, os.WriteFile(stateFile, test.state, 0o600))
			}

			ctx := running.RunningCtx{Instances: []running.InstanceCtx{inst}}
			printer := &recordingPrinter{}

			require.NoError(t, Status(ctx, printer, DefaultInstanceTimeout))

			storage := printer.printed["myapp:storage-1"]
			require.NotNil(t, storage)
			assert.Equal(t, process_utils.ProcStateRunning.Status, storage.Status)
			assert.Nil(t, storage.PID)
			require.NotNil(t, storage.WatchdogPID)
			assert.Equal(t, os.Getpid(), *storage.WatchdogPID)
			assert.Nil(t, storage.StartedAt)
			assert.Nil(t, storage.Restarts)

			out := captureStdout(t, func() error {
				return Status(ctx, NewJSONPrinter(), DefaultInstanceTimeout)
			})

			var decoded map[string]map[string]any

			require.NoError(t, json.Unmarshal([]byte(out), &decoded))
			assert.Contains(t, decoded["myapp:storage-1"], "pid")
			assert.Nil(t, decoded["myapp:storage-1"]["pid"])
			assert.InDelta(t, os.Getpid(), decoded["myapp:storage-1"]["watchdog_pid"], 0)
			assert.NotContains(t, decoded["myapp:storage-1"], "restarts")

			table := captureStdout(t, func() error {
				return Status(ctx, NewTablePrinter(WithDetails(true)), DefaultInstanceTimeout)
			})
			assert.Contains(t, table,
				fmt.Sprintf("Watchdog of myapp:storage-1: PID %d\n", os.Getpid()))
			assert.Contains(t, table, test.note)
			assert.NotContains(t, table, "Process of myapp:storage-1")
		})
	}
}

// TestStatusNotRunningHasNoPIDs pins that an instance that does not run has
// neither PID: both are null in JSON, and the details show no watchdog.
func TestStatusNotRunningHasNoPIDs(t *testing.T) {
	dir := t.TempDir()
	inst := running.InstanceCtx{
		AppName: "myapp", InstName: "worker-1", Kind: instances.KindExternal,
		PIDFile: filepath.Join(dir, "tt.pid"), RunDir: dir,
	}

	// The state a watchdog that died left does not make it run.
	publishState(t, &inst, watchdogState(0))

	ctx := running.RunningCtx{Instances: []running.InstanceCtx{inst}}

	out := captureStdout(t, func() error {
		return Status(ctx, NewJSONPrinter(), DefaultInstanceTimeout)
	})

	var decoded map[string]map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, "NOT RUNNING", decoded["myapp:worker-1"]["status"])
	assert.Contains(t, decoded["myapp:worker-1"], "watchdog_pid")
	assert.Nil(t, decoded["myapp:worker-1"]["watchdog_pid"])
	assert.Nil(t, decoded["myapp:worker-1"]["pid"])
	assert.NotContains(t, decoded["myapp:worker-1"], "restarts")

	table := captureStdout(t, func() error {
		return Status(ctx, NewTablePrinter(WithDetails(true)), DefaultInstanceTimeout)
	})
	assert.NotContains(t, table, "Watchdog of")
	assert.NotContains(t, table, "Process of")
}

// TestStatusOrphan pins that an external process whose watchdog is gone
// while its process runs on is not shown running: nothing supervises it. An
// alert names the process and how to stop it.
func TestStatusOrphan(t *testing.T) {
	process := exec.CommandContext(context.Background(), "sleep", "60")

	process.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, process.Start())

	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	})

	dir := t.TempDir()
	inst := running.InstanceCtx{
		AppName:        "myapp",
		InstName:       "worker-1",
		Kind:           instances.KindExternal,
		PIDFile:        filepath.Join(dir, "tt.pid"),
		ProcessPIDFile: filepath.Join(dir, "process.pid"),
	}

	require.NoError(t, os.WriteFile(inst.ProcessPIDFile,
		[]byte(strconv.Itoa(process.Process.Pid)), 0o600))

	printer := &recordingPrinter{}

	require.NoError(t, Status(running.RunningCtx{Instances: []running.InstanceCtx{inst}},
		printer, DefaultInstanceTimeout))

	worker := printer.printed["myapp:worker-1"]
	require.NotNil(t, worker)
	assert.Equal(t, process_utils.ProcStateStopped.Status, worker.Status)
	assert.Nil(t, worker.PID)
	assert.Nil(t, worker.WatchdogPID)
	require.Len(t, worker.Alerts, 1)
	assert.Equal(t, severityError, worker.Alerts[0].Severity)
	assert.Contains(t, worker.Alerts[0].Message, strconv.Itoa(process.Process.Pid))
	assert.Contains(t, worker.Alerts[0].Message, "runs without its watchdog")
	assert.Contains(t, worker.Alerts[0].Message, "tt instance kill myapp:worker-1")

	// An instance that a unit manages is stopped through its unit.
	inst.Unit = "myapp@worker-1.service"

	require.NoError(t, Status(running.RunningCtx{Instances: []running.InstanceCtx{inst}},
		printer, DefaultInstanceTimeout))

	worker = printer.printed["myapp:worker-1"]
	require.NotNil(t, worker)
	require.Len(t, worker.Alerts, 1)
	assert.Contains(t, worker.Alerts[0].Message,
		"stop it with systemctl stop myapp@worker-1.service")
}

// TestStatusNotInConfig pins that an instance that runs while the config no
// longer has it shows so: a note under the table that names it, a warning
// among its alerts, and not_in_config in JSON, which the others lack.
func TestStatusNotInConfig(t *testing.T) {
	orphan := runningInstance(t, "storage-2", instances.KindCluster)

	orphan.NotInConfig = true

	member := runningInstance(t, "storage-1", instances.KindCluster)
	both := running.RunningCtx{Instances: []running.InstanceCtx{member, orphan}}

	printer := &recordingPrinter{}

	require.NoError(t, Status(both, printer, DefaultInstanceTimeout))
	require.NotNil(t, printer.printed["myapp:storage-2"])
	assert.True(t, printer.printed["myapp:storage-2"].NotInConfig)
	assert.Contains(t, printer.printed["myapp:storage-2"].Alerts, instanceAlert{
		Message: "myapp:storage-2 is not in the config: it runs on from before it was " +
			"removed from the topology; stop it with tt instance stop myapp:storage-2",
		Severity: severityWarning,
	})
	assert.False(t, printer.printed["myapp:storage-1"].NotInConfig)

	table := captureStdout(t, func() error {
		return Status(both, NewTablePrinter(), DefaultInstanceTimeout)
	})
	assert.Regexp(t, `(?m)^\s*myapp:storage-2\s+RUNNING\s+`+strconv.Itoa(testProcessPID)+`\s`,
		table, "the columns stay as they are")
	assert.Contains(t, table, "\nNot in the config: myapp:storage-2.\n")
	assert.Contains(t, table, "tt instance stop stops them")

	table = captureStdout(t, func() error {
		return Status(running.RunningCtx{Instances: []running.InstanceCtx{member}},
			NewTablePrinter(), DefaultInstanceTimeout)
	})
	assert.NotContains(t, table, "Not in the config")

	out := captureStdout(t, func() error {
		return Status(both, NewJSONPrinter(), DefaultInstanceTimeout)
	})

	var statuses map[string]map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &statuses))
	assert.Equal(t, true, statuses["myapp:storage-2"]["not_in_config"])
	assert.NotContains(t, statuses["myapp:storage-1"], "not_in_config")
}

// captureStdout returns what run writes to the standard output.
func captureStdout(t *testing.T, run func() error) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	require.NoError(t, err)

	previous := os.Stdout

	os.Stdout = writer

	output := make(chan string)

	go func() {
		data, _ := io.ReadAll(reader)
		output <- string(data)
	}()

	runErr := run()

	os.Stdout = previous

	require.NoError(t, writer.Close())
	require.NoError(t, runErr)

	return <-output
}

// TestStatusUnits pins that tt instance status shows the systemd unit that
// manages an instance: in the UNIT column, right after STATUS, which is there
// only when a unit manages some of the instances, and in the unit field of
// JSON and YAML, which is left out for an instance without one.
func TestStatusUnits(t *testing.T) {
	managed := runningInstance(t, "worker-1", instances.KindExternal)

	managed.Unit = "myapp@worker-1.service"

	stopped := running.InstanceCtx{
		AppName:  "myapp",
		InstName: "worker-2",
		Kind:     instances.KindExternal,
		PIDFile:  filepath.Join(t.TempDir(), "tt.pid"),
	}

	both := running.RunningCtx{Instances: []running.InstanceCtx{managed, stopped}}

	table := captureStdout(t, func() error {
		return Status(both, NewTablePrinter(), DefaultInstanceTimeout)
	})
	lines := strings.Split(table, "\n")
	require.GreaterOrEqual(t, len(lines), 3, table)
	assert.Regexp(t, `^\s*INSTANCE\s+STATUS\s+UNIT\s+PID\s+MODE\s`, lines[0])
	assert.Regexp(t, `^\s*myapp:worker-1\s+RUNNING\s+myapp@worker-1\.service\s+`+
		strconv.Itoa(testProcessPID)+`\s`, lines[1])
	assert.Regexp(t, `^\s*myapp:worker-2\s+NOT RUNNING\s*$`, lines[2])

	table = captureStdout(t, func() error {
		return Status(running.RunningCtx{Instances: []running.InstanceCtx{stopped}},
			NewTablePrinter(), DefaultInstanceTimeout)
	})
	assert.NotContains(t, table, "UNIT")

	out := captureStdout(t, func() error {
		return Status(both, NewJSONPrinter(), DefaultInstanceTimeout)
	})

	var statuses map[string]map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &statuses))
	assert.Equal(t, "myapp@worker-1.service", statuses["myapp:worker-1"]["unit"])
	assert.NotContains(t, statuses["myapp:worker-2"], "unit")
}

// TestStatusDetailsTarantool pins that the table with --details names the
// tarantool every Tarantool instance starts on and where it comes from, and
// that nothing else shows it: neither the table without --details nor JSON
// and YAML.
func TestStatusDetailsTarantool(t *testing.T) {
	bin := t.TempDir()
	exe := filepath.Join(bin, "tarantool")

	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", bin)
	t.Setenv("TT_USE_SYSTEM_TARANTOOL", "")

	storage := running.InstanceCtx{
		AppName:  "myapp",
		InstName: "storage-1",
		Kind:     instances.KindCluster,
		PIDFile:  filepath.Join(t.TempDir(), "tt.pid"),
		Scope:    "project",
		AppDir:   t.TempDir(),
	}
	both := running.RunningCtx{Instances: []running.InstanceCtx{
		storage, runningInstance(t, "worker-1", instances.KindExternal),
	}}

	table := captureStdout(t, func() error {
		return Status(both, NewTablePrinter(WithDetails(true)), DefaultInstanceTimeout)
	})
	assert.Contains(t, table,
		"Tarantool for myapp:storage-1: "+exe+", the first tarantool on PATH\n")
	assert.NotContains(t, table, "Tarantool for myapp:worker-1")

	for _, printer := range []InstanceStatusPrinter{
		NewTablePrinter(), NewTablePrinter(WithPretty()), NewJSONPrinter(), NewYAMLPrinter(),
	} {
		out := captureStdout(t, func() error {
			return Status(both, printer, DefaultInstanceTimeout)
		})
		assert.NotContains(t, out, exe)
	}
}

// versionDir lays an installed version of myapp holding its VERSION.
func versionDir(t *testing.T, root, version string) string {
	t.Helper()

	dir := filepath.Join(root, "versions", "myapp", "myapp_"+version)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "VERSION"), []byte(version+"\n"), 0o600))

	return dir
}

// TestStatusVersion pins that a running instance of an application of the
// user or system scope shows the version its watchdog recorded it runs, and
// a restart pending once another version is active; an instance with no
// record, or of a project, shows none.
func TestStatusVersion(t *testing.T) {
	root := t.TempDir()
	old := versionDir(t, root, "1.0.0")
	active := versionDir(t, root, "1.1.0")

	// An instance of the user scope, running from active, whose watchdog
	// recorded it runs from recorded, if it is not empty.
	instance := func(name, recorded string) running.InstanceCtx {
		inst := runningInstance(t, name, instances.KindExternal)

		inst.Scope = "user"
		inst.AppDir = active
		inst.RunDir = filepath.Dir(inst.PIDFile)

		if recorded != "" {
			require.NoError(t, os.WriteFile(filepath.Join(inst.RunDir, "app.dir"),
				[]byte(recorded+"\n"), 0o600))
		}

		return inst
	}

	current := instance("current", active)

	current.Scope = "system"

	project := instance("project", old)

	project.Scope = "project"

	all := running.RunningCtx{Instances: []running.InstanceCtx{
		instance("behind", old), current, instance("unknown", ""), project,
	}}

	printer := &recordingPrinter{}
	require.NoError(t, Status(all, printer, DefaultInstanceTimeout))

	assert.Equal(t, "1.0.0", printer.printed["myapp:behind"].Version)
	assert.True(t, printer.printed["myapp:behind"].RestartPending)
	assert.Equal(t, "1.1.0", printer.printed["myapp:current"].Version)
	assert.False(t, printer.printed["myapp:current"].RestartPending)
	assert.Empty(t, printer.printed["myapp:unknown"].Version)
	assert.Empty(t, printer.printed["myapp:project"].Version)

	out := captureStdout(t, func() error {
		return Status(all, NewJSONPrinter(), DefaultInstanceTimeout)
	})

	var decoded map[string]map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, "1.0.0", decoded["myapp:behind"]["version"])
	assert.Equal(t, true, decoded["myapp:behind"]["restart_pending"])
	assert.NotContains(t, decoded["myapp:current"], "restart_pending")
	assert.NotContains(t, decoded["myapp:unknown"], "version")

	table := captureStdout(t, func() error {
		return Status(all, NewTablePrinter(WithDetails(true)), DefaultInstanceTimeout)
	})
	assert.Contains(t, table, "Version of myapp:behind: 1.0.0, restart pending: 1.1.0 is active\n")
	assert.Contains(t, table, "Version of myapp:current: 1.1.0, the active version\n")
	assert.NotContains(t, table, "Version of myapp:unknown")
	assert.NotContains(t, table, "Version of myapp:project")

	table = captureStdout(t, func() error {
		return Status(all, NewTablePrinter(), DefaultInstanceTimeout)
	})
	assert.Contains(t, table, "Some instances do not run the active version")
	assert.NotContains(t, table, "Version of")
}

func TestStatusDetailsLastExit(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "tt.log")
	require.NoError(t, os.WriteFile(logFile,
		[]byte("a\nb\nc\nd\nF> can't initialize storage\n"+
			"Watchdog (WARN): tarantool, PID 1, has ended: it exited with code 1.\n"),
		0o644))

	exitedAt := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	failed := newInstanceStatus()

	failed.LastExit = &running.LastExit{At: exitedAt, Exited: "it exited with code 1"}
	failed.log = logFile

	stopped := newInstanceStatus()

	stopped.LastExit = &running.LastExit{
		At: exitedAt, Exited: "it exited with code 0", Stopped: true,
	}
	stopped.log = logFile

	never := newInstanceStatus()

	never.log = filepath.Join(dir, "none.log")

	instances := map[string]*instanceStatus{
		"app:failed": &failed, "app:stopped": &stopped, "app:never": &never,
	}
	for _, instStatus := range instances {
		instStatus.procStatus = process_utils.ProcStateStopped
	}

	out := captureStdout(t, func() error {
		return NewTablePrinter(WithDetails(true)).Print(instances)
	})

	assert.Contains(t, out,
		"Last exit of app:failed: it exited with code 1, at 2026-11-17 10:00:00\n")
	assert.Contains(t, out, "  The last lines of "+logFile+":\n")
	assert.Contains(t, out, "    F> can't initialize storage\n")
	assert.NotContains(t, out, "    a\n", "five lines are quoted")
	assert.Contains(t, out, "  tt instance logs app:failed prints more.\n")
	// A clean stop needs no log.
	assert.Contains(t, out,
		"Last exit of app:stopped: stopped, it exited with code 0, at 2026-11-17 10:00:00\n")
	assert.NotContains(t, out, "tt instance logs app:stopped")
	assert.NotContains(t, out, "app:never:")

	out = captureStdout(t, func() error {
		return NewTablePrinter().Print(instances)
	})
	assert.NotContains(t, out, "Last exit")
	assert.Contains(t, out, "Some instances are not running and were not stopped.")

	delete(instances, "app:failed")

	out = captureStdout(t, func() error {
		return NewTablePrinter().Print(instances)
	})
	assert.NotContains(t, out, "were not stopped")
}

func TestConfigStatus(t *testing.T) {
	script := running.InstanceCtx{Kind: instances.KindScript}
	cluster := running.InstanceCtx{Kind: instances.KindCluster}

	// The config module of an instance that runs its script is never used.
	assert.Equal(t, "--", configStatus(&script, "uninitialized"))
	assert.Equal(t, "--", configStatus(&script, ""))
	// A script that starts the config module itself shows what it tells.
	assert.Equal(t, "ready", configStatus(&script, "ready"))

	assert.Equal(t, "uninitialized", configStatus(&cluster, "uninitialized"))
	assert.Equal(t, "ready", configStatus(&cluster, "ready"))
}
