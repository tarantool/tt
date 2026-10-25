package running

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/ttlog"
)

var (
	errTarantoolExited    = errors.New("tarantool exited: ")
	errTimedOutWaitingFor = errors.New("timed out waiting for ")
)

const stopTimeout = 5 * time.Second

// waitForEventLoop waits for tarantool to report that it has entered its event
// loop.
func waitForEventLoop(reader io.Reader) error {
	const (
		msgToWait = "entering the event loop"
		waitFor   = 10 * time.Second
	)

	buf := bufio.NewReader(reader)
	waitUntil := time.Now().Add(waitFor)

	// line collects a line that arrives in parts.
	var line, previousLine string

	for {
		part, err := buf.ReadString('\n')

		line += part

		switch {
		case errors.Is(err, io.EOF): // No complete line yet. Wait.
			if time.Now().After(waitUntil) {
				return fmt.Errorf("%w%q", errTimedOutWaitingFor, msgToWait)
			}

			time.Sleep(100 * time.Millisecond)

			continue
		case err != nil:
			return err
		case strings.Contains(line, msgToWait):
			return nil
		case strings.Contains(line, "exiting"):
			return fmt.Errorf("%w%q", errTarantoolExited, previousLine)
		}

		previousLine, line = line, ""
	}
}

// bufferOptions sends the output of tarantool to buf, through a logger as the
// watchdog does. The output is copied into buf while the test reads it.
func bufferOptions(buf *lockedBuffer) specOptions {
	logger := ttlog.NewCustomLogger(buf, "test", 0)

	return specOptions{integrity: nil, stdout: logger, stderr: logger}
}

// clusterAppConfig is a cluster config of three instances: instance-001
// keeps the defaults tt gives, instance-002 moves its console socket and its
// write-ahead log into the project directory, and all listen on iproto
// sockets in the project directory.
const clusterAppConfig = `credentials:
  users:
    guest:
      roles: [super]

iproto:
  listen:
    - uri: 'unix/:./{{ instance_name }}.iproto'

groups:
  group-001:
    replicasets:
      replicaset-001:
        instances:
          instance-001:
            database:
              mode: rw
      replicaset-002:
        instances:
          instance-002:
            console:
              socket: ./{{instance_name}}.control
            wal:
              dir: ./{{instance_name}}_wal_dir
            database:
              mode: rw
`

// clusterAppInstance lays out a project whose pair runs clusterAppConfig and
// returns the context of instName, with the directories of its layout
// created as a start creates them.
func clusterAppInstance(t *testing.T, instName string) InstanceCtx {
	t.Helper()

	setScopeRoots(t)

	dir := filepath.Join(shortTempDir(t), fixtureApp)
	writeFiles(t, dir, map[string]string{
		"app.manifest.toml":                 fixtureManifest,
		"app.manifest.lock":                 fixtureLock,
		".templates/default/instances.yaml": "instance-001:\ninstance-002:\n",
		".templates/default/config.yaml":    clusterAppConfig,
	})

	insts, err := Collect(t.Context(), []app.Target{{App: fixtureApp, Instance: instName}},
		app.Options{Start: dir})
	require.NoError(t, err)
	require.Len(t, insts, 1)
	require.NoError(t, createInstanceDataDirectories(insts[0]))

	return insts[0]
}

// TestClusterInstanceStart starts an instance of a project with the
// defaults tt gives: the sockets and the pid file under the run directory of
// the layout, the data under its data directory, the log to the output, and
// what the config sets, the iproto socket, where the config says.
func TestClusterInstanceStart(t *testing.T) {
	inst := clusterAppInstance(t, "instance-001")
	outputBuf := &lockedBuffer{}

	spec, err := instanceSpec(&inst, bufferOptions(outputBuf))
	require.NoError(t, err)

	clusterInstance := startSpec(t, context.Background(), spec)
	t.Cleanup(func() {
		clusterInstance.stop()
	})
	require.NoError(t, waitForEventLoop(outputBuf))
	assert.FileExists(t, filepath.Join(inst.RunDir, "tarantool.control"))
	assert.FileExists(t, filepath.Join(inst.RunDir, "tarantool.pid"))
	assert.FileExists(t, filepath.Join(inst.AppDir, "instance-001.iproto"))
	assert.NoFileExists(t, filepath.Join(inst.LogDir, "tarantool.log"))
	assert.Eventually(t, func() bool {
		snapshots, _ := filepath.Glob(filepath.Join(inst.WalDir, "*.snap"))

		return len(snapshots) > 0
	}, 10*time.Second, 50*time.Millisecond, "no snapshot in the data directory")
	assert.NoDirExists(t, filepath.Join(inst.AppDir, "instance-001"))
	assert.NoDirExists(t, filepath.Join(inst.AppDir, "var"))
}

// TestClusterInstanceConfigPaths pins that the paths the config sets win
// over the defaults of the layout, relative ones resolved against the
// project directory, and the rest keep the defaults.
func TestClusterInstanceConfigPaths(t *testing.T) {
	inst := clusterAppInstance(t, "instance-002")
	outputBuf := &lockedBuffer{}

	assert.Equal(t, filepath.Join(inst.AppDir, "instance-002.control"), inst.ConsoleSocket)
	assert.Equal(t, filepath.Join(inst.AppDir, "instance-002_wal_dir"), inst.WalDir)

	spec, err := instanceSpec(&inst, bufferOptions(outputBuf))
	require.NoError(t, err)

	clusterInstance := startSpec(t, context.Background(), spec)
	t.Cleanup(func() {
		clusterInstance.stop()
	})
	require.NoError(t, waitForEventLoop(outputBuf))

	assert.FileExists(t, filepath.Join(inst.AppDir, "instance-002.control"))
	assert.NoFileExists(t, filepath.Join(inst.RunDir, "tarantool.control"))
	assert.FileExists(t, filepath.Join(inst.RunDir, "tarantool.pid"))
	assert.DirExists(t, filepath.Join(inst.AppDir, "instance-002_wal_dir"))
	assert.Eventually(t, func() bool {
		snapshots, _ := filepath.Glob(filepath.Join(inst.MemtxDir, "*.snap"))

		return len(snapshots) > 0
	}, 10*time.Second, 50*time.Millisecond, "no snapshot in the data directory")
}

// TestClusterInstanceStopByContext pins that an instance run in the
// foreground stops cleanly when its context is cancelled.
func TestClusterInstanceStopByContext(t *testing.T) {
	inst := clusterAppInstance(t, "instance-001")
	outputBuf := &lockedBuffer{}

	spec, err := instanceSpec(&inst, bufferOptions(outputBuf))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	clusterInstance := startSpec(t, ctx, spec)
	t.Cleanup(func() {
		clusterInstance.stop()
	})
	require.NoError(t, waitForEventLoop(outputBuf))
	cancel()
	require.ErrorIs(t, clusterInstance.wait(), context.Canceled)
	assert.True(t, clusterInstance.cmd.ProcessState.Success())
}
