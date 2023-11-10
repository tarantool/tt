package running

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/cli/util"
)

var tntCli cmdcontext.TarantoolCli

const stopTimeout = 5 * time.Second

func waitForMsgInBuffer(reader io.Reader, msgToWait string, waitFor time.Duration) error {
	buf := bufio.NewReader(reader)
	waitUntil := time.Now().Add(waitFor)
	var previousLine string
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			if err == io.EOF { // No output yet. Wait.
				time.Sleep(100 * time.Millisecond)
			} else {
				return err
			}
		}
		if strings.Contains(line, msgToWait) {
			break
		} else if strings.Contains(line, "exiting") {
			return fmt.Errorf("tarantool exited: %q", previousLine)
		}
		previousLine = line
		if time.Now().After(waitUntil) { // Timeout.
			return fmt.Errorf("timed out waiting for %q", msgToWait)
		}
	}
	return nil
}

func SkipForTntMajorBefore3(t *testing.T) {
	tntCli.Executable = "tarantool"
	tntVersion, err := tntCli.GetVersion()
	require.NoError(t, err)
	if tntVersion.Major < 3 {
		t.Skipf("cluster instances test is skipped for tarantool version %s", tntVersion.Str)
	}
}

func TestClusterInstance_Start(t *testing.T) {
	SkipForTntMajorBefore3(t)

	configPath, err := filepath.Abs(filepath.Join("testdata", "instances_enabled",
		"cluster_app", "config.yml"))
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cancelChdir, err := util.Chdir(tmpDir)
	require.NoError(t, err)
	defer cancelChdir()

	outputBuf := bytes.Buffer{}
	outputBuf.Grow(1024)
	clusterInstance, err := newClusterInstance(tntCli, InstanceCtx{
		ClusterConfigPath: configPath,
		InstName:          "instance-001",
		AppDir:            tmpDir,
	}, ttlog.NewCustomLogger(&outputBuf, "test", 0))

	require.NoError(t, err)
	require.NotNil(t, clusterInstance)
	require.NoError(t, clusterInstance.Start())
	t.Cleanup(func() {
		require.NoError(t, clusterInstance.Stop(stopTimeout))
	})
	require.NoError(t, waitForMsgInBuffer(&outputBuf, "entering the event loop", 10*time.Second))
	assert.FileExists(t, filepath.Join(tmpDir, "var", "run", "instance-001", "tarantool.control"))
	assert.FileExists(t, filepath.Join(tmpDir, "var", "run", "instance-001", "tarantool.pid"))
	assert.FileExists(t, filepath.Join(tmpDir, "instance-001.iproto"))
	assert.NoFileExists(t, filepath.Join(tmpDir, "var", "log", "instance-001", "tarantool.log"))
	assert.DirExists(t, filepath.Join(tmpDir, "var", "lib", "instance-001"))
	assert.NoDirExists(t, filepath.Join(tmpDir, "instance-001"))
}

func TestClusterInstance_StartChangeDefaults(t *testing.T) {
	SkipForTntMajorBefore3(t)

	configPath, err := filepath.Abs(filepath.Join("testdata", "instances_enabled",
		"cluster_app", "config.yml"))
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cancelChdir, err := util.Chdir(tmpDir)
	require.NoError(t, err)
	defer cancelChdir()

	tmpAppDir := filepath.Join(tmpDir, "appdir")
	require.NoError(t, os.Mkdir(tmpAppDir, 0755))
	outputBuf := bytes.Buffer{}
	outputBuf.Grow(1024)
	clusterInstance, err := newClusterInstance(tntCli, InstanceCtx{
		ClusterConfigPath: configPath,
		InstName:          "instance-001",
		WalDir:            "wal_dir",
		MemtxDir:          "snap_dir",
		VinylDir:          "vinyl_dir",
		ConsoleSocket:     "run/tt.control",
		AppDir:            tmpAppDir,
	}, ttlog.NewCustomLogger(&outputBuf, "test", 0))
	require.NoError(t, err)
	require.NotNil(t, clusterInstance)

	require.NoError(t, os.Mkdir(filepath.Join(tmpAppDir, "run"), 0755))
	require.NoError(t, clusterInstance.Start())
	t.Cleanup(func() {
		require.NoError(t, clusterInstance.Stop(stopTimeout))
	})
	require.NoError(t, waitForMsgInBuffer(&outputBuf, "entering the event loop", 10*time.Second))
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
	SkipForTntMajorBefore3(t)

	configPath, err := filepath.Abs(filepath.Join("testdata", "instances_enabled",
		"cluster_app", "config.yml"))
	require.NoError(t, err)

	tmpDir := t.TempDir()
	cancelChdir, err := util.Chdir(tmpDir)
	require.NoError(t, err)
	defer cancelChdir()

	tmpAppDir := filepath.Join(tmpDir, "appdir")
	require.NoError(t, os.Mkdir(tmpAppDir, 0755))
	outputBuf := bytes.Buffer{}
	outputBuf.Grow(1024)
	clusterInstance, err := newClusterInstance(tntCli, InstanceCtx{
		ClusterConfigPath: configPath,
		InstName:          "instance-002",
		WalDir:            "wal_dir",
		MemtxDir:          "snap_dir",
		VinylDir:          "vinyl_dir",
		ConsoleSocket:     "run/tt.control",
		AppDir:            tmpAppDir,
		LogDir:            tmpAppDir,
	}, ttlog.NewCustomLogger(&outputBuf, "test", 0))
	require.NoError(t, err)
	require.NotNil(t, clusterInstance)

	require.NoError(t, os.Mkdir(filepath.Join(tmpAppDir, "run"), 0755))
	require.NoError(t, clusterInstance.Start())
	t.Cleanup(func() {
		require.NoError(t, clusterInstance.Stop(stopTimeout))
	})
	require.NoError(t, waitForMsgInBuffer(&outputBuf, "entering the event loop", 10*time.Second))

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
