package running

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/ttlog"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errTarantoolExited    = errors.New("tarantool exited: ")
	errTimedOutWaitingFor = errors.New("timed out waiting for ")
)

var tntCli = cmdcontext.TarantoolCli{Executable: "tarantool"}

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

func TestClusterInstance_Start(t *testing.T) {
	configPath, err := filepath.Abs(filepath.Join("testdata", "applications",
		"cluster_app", "config.yml"))
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cancelChdir, err := util.Chdir(tmpDir)
	require.NoError(t, err)

	defer func() {
		_ = cancelChdir()
	}()

	outputBuf := &lockedBuffer{}

	spec, err := clusterSpec(tntCli.Executable, &InstanceCtx{
		ClusterConfigPath: configPath,
		InstName:          "instance-001",
		AppDir:            tmpDir,
		BinaryPort:        "localhost:3013",
	}, bufferOptions(outputBuf))

	require.NoError(t, err)

	clusterInstance := startSpec(t, context.Background(), spec)
	t.Cleanup(func() {
		clusterInstance.stop()
	})
	require.NoError(t, waitForEventLoop(outputBuf))
	assert.FileExists(t, filepath.Join(tmpDir, "var", "run", "instance-001", "tarantool.control"))
	assert.FileExists(t, filepath.Join(tmpDir, "var", "run", "instance-001", "tarantool.pid"))
	assert.FileExists(t, filepath.Join(tmpDir, "instance-001.iproto"))
	assert.NoFileExists(t, filepath.Join(tmpDir, "var", "log", "instance-001", "tarantool.log"))
	assert.DirExists(t, filepath.Join(tmpDir, "var", "lib", "instance-001"))
	assert.NoDirExists(t, filepath.Join(tmpDir, "instance-001"))
}

func TestClusterInstance_StartChangeDefaults(t *testing.T) {
	configPath, err := filepath.Abs(filepath.Join("testdata", "applications",
		"cluster_app", "config.yml"))
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cancelChdir, err := util.Chdir(tmpDir)
	require.NoError(t, err)

	defer func() {
		_ = cancelChdir()
	}()

	tmpAppDir := filepath.Join(tmpDir, "appdir")
	require.NoError(t, os.Mkdir(tmpAppDir, 0o755))

	outputBuf := &lockedBuffer{}

	spec, err := clusterSpec(tntCli.Executable, &InstanceCtx{
		ClusterConfigPath: configPath,
		InstName:          "instance-001",
		WalDir:            "wal_dir",
		MemtxDir:          "snap_dir",
		VinylDir:          "vinyl_dir",
		ConsoleSocket:     "run/tt.control",
		AppDir:            tmpAppDir,
		BinaryPort:        "localhost:3013",
	}, bufferOptions(outputBuf))

	require.NoError(t, err)

	require.NoError(t, os.Mkdir(filepath.Join(tmpAppDir, "run"), 0o755))

	clusterInstance := startSpec(t, context.Background(), spec)
	t.Cleanup(func() {
		clusterInstance.stop()
	})
	require.NoError(t, waitForEventLoop(outputBuf))
	assert.FileExists(t, filepath.Join(tmpAppDir, "run", "tt.control"))
	assert.NoFileExists(t, filepath.Join(tmpAppDir, "var", "run",
		"instance-001", "instance-001.control"))
	assert.FileExists(t, filepath.Join(tmpAppDir, "var", "run", "instance-001", "tarantool.pid"))
	assert.FileExists(t, filepath.Join(tmpAppDir, "instance-001.iproto"))
	assert.DirExists(t, filepath.Join(tmpAppDir, "wal_dir"))
	assert.DirExists(t, filepath.Join(tmpAppDir, "snap_dir"))
	assert.DirExists(t, filepath.Join(tmpAppDir, "vinyl_dir"))
	assert.NoDirExists(t, filepath.Join(tmpAppDir, "instance-001"))
}

func TestClusterInstance_StartChangeSomeDefaults(t *testing.T) {
	configPath, err := filepath.Abs(filepath.Join("testdata", "applications",
		"cluster_app", "config.yml"))
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cancelChdir, err := util.Chdir(tmpDir)
	require.NoError(t, err)

	defer func() {
		_ = cancelChdir()
	}()

	tmpAppDir := filepath.Join(tmpDir, "appdir")
	require.NoError(t, os.Mkdir(tmpAppDir, 0o755))

	outputBuf := &lockedBuffer{}

	spec, err := clusterSpec(tntCli.Executable, &InstanceCtx{
		ClusterConfigPath: configPath,
		InstName:          "instance-002",
		WalDir:            "wal_dir",
		MemtxDir:          "snap_dir",
		VinylDir:          "vinyl_dir",
		ConsoleSocket:     "run/tt.control",
		AppDir:            tmpAppDir,
		LogDir:            tmpAppDir,
		BinaryPort:        "localhost:3013",
	}, bufferOptions(outputBuf))

	require.NoError(t, err)

	require.NoError(t, os.Mkdir(filepath.Join(tmpAppDir, "run"), 0o755))

	clusterInstance := startSpec(t, context.Background(), spec)
	t.Cleanup(func() {
		clusterInstance.stop()
	})
	require.NoError(t, waitForEventLoop(outputBuf))

	assert.NoFileExists(t, filepath.Join(tmpAppDir, "run", "tt.control"))
	assert.NoFileExists(t, filepath.Join(tmpAppDir, "var", "run",
		"instance-001", "instance-001.control"))
	assert.FileExists(t, filepath.Join(tmpAppDir, "instance-002.control")) // From config.

	assert.FileExists(t, filepath.Join(tmpAppDir, "var", "run", "instance-002", "tarantool.pid"))
	assert.FileExists(t, filepath.Join(tmpAppDir, "instance-002.iproto"))

	assert.NoDirExists(t, filepath.Join(tmpAppDir, "wal_dir"))
	assert.DirExists(t, filepath.Join(tmpAppDir, "instance-002_wal_dir")) // From config.

	assert.DirExists(t, filepath.Join(tmpAppDir, "snap_dir"))
	assert.DirExists(t, filepath.Join(tmpAppDir, "vinyl_dir"))
	assert.NoDirExists(t, filepath.Join(tmpAppDir, "instance-002"))
}

func TestClusterInstance_StopByContext(t *testing.T) {
	configPath, err := filepath.Abs(filepath.Join("testdata", "applications",
		"cluster_app", "config.yml"))
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cancelChdir, err := util.Chdir(tmpDir)
	require.NoError(t, err)

	defer func() {
		_ = cancelChdir()
	}()

	outputBuf := &lockedBuffer{}

	spec, err := clusterSpec(tntCli.Executable, &InstanceCtx{
		ClusterConfigPath: configPath,
		InstName:          "instance-001",
		AppDir:            tmpDir,
		BinaryPort:        "localhost:3013",
	}, bufferOptions(outputBuf))

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
