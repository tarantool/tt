package status

import (
	"bufio"
	"context"
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

// runningInstance returns the context of the instance inst of myapp of kind,
// whose pid file names the test process, so that it counts as running, and
// whose console socket does not exist.
func runningInstance(t *testing.T, inst string, kind instances.Kind) running.InstanceCtx {
	t.Helper()

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "tt.pid")

	require.NoError(t, os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600))

	ctx := running.InstanceCtx{
		AppName:  "myapp",
		InstName: inst,
		Kind:     kind,
		PIDFile:  pidFile,
	}

	if kind != instances.KindExternal {
		ctx.ConsoleSocket = filepath.Join(dir, "tarantool.control")
	}

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
	assert.Equal(t, os.Getpid(), *worker.PID)
	assert.Empty(t, worker.Alerts)
	assert.Empty(t, worker.Mode)
	assert.Equal(t, defaultModuleStatus, worker.Config)
	assert.Equal(t, defaultModuleStatus, worker.Box)
	assert.Equal(t, defaultModuleStatus, worker.Upstream)
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
	require.Len(t, worker.Alerts, 1)
	assert.Equal(t, severityError, worker.Alerts[0].Severity)
	assert.Contains(t, worker.Alerts[0].Message, strconv.Itoa(process.Process.Pid))
	assert.Contains(t, worker.Alerts[0].Message, "runs without its watchdog")
	assert.Contains(t, worker.Alerts[0].Message, "tt instance kill myapp:worker-1")
}
