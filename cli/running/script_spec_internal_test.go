package running

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/ttlog"
)

const (
	instTestAppDir = "./test_app"
)

// startTestInstance starts instance for the test.
func startTestInstance(t *testing.T, ctx context.Context, app, consoleSock string,
	binaryPort string, logger ttlog.Logger,
) *specRun {
	t.Helper()

	// Need absolute path to the script, because working dir is changed on start.
	appPath, err := filepath.Abs(filepath.Join(instTestAppDir, app+".lua"))
	require.NoErrorf(t, err, `Unknown application: "%v". Error: "%v".`, appPath, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	require.NoErrorf(t, err, `Can't find a tarantool binary. Error: "%v".`, err)

	instTestDataDir := t.TempDir()
	binPath, err := os.Executable()
	require.NoError(t, err)

	binDir := filepath.Dir(binPath)

	// The Spec takes the environment as it is when the Spec is built.
	t.Setenv("started_flag_file", filepath.Join(binDir, app))

	spec, err := scriptSpec(tarantoolBin, &InstanceCtx{
		AppDir:         binDir,
		InstanceScript: appPath,
		ConsoleSocket:  consoleSock,
		WalDir:         instTestDataDir,
		VinylDir:       instTestDataDir,
		MemtxDir:       instTestDataDir,
		BinaryPort:     binaryPort,
	}, specOptions{integrity: nil, stdout: logger, stderr: logger})
	require.NoErrorf(t, err, `Can't create an instance. Error: "%v".`, err)

	defer func() {
		_ = os.Remove(os.Getenv("started_flag_file"))
	}()

	run := startSpec(t, ctx, spec)

	require.NotZero(t, waitForFile(os.Getenv("started_flag_file")), "Instance is not started")
	assert.True(t, run.alive(), "Can't start the instance.")

	return run
}

// cleanupTestInstance stops the instance if it still runs after the test,
// and removes its console socket.
func cleanupTestInstance(t *testing.T, run *specRun, consoleSock string) {
	t.Helper()

	run.stop()

	_, err := os.Stat(consoleSock)
	if err == nil {
		_ = os.Remove(consoleSock)
	}
}

func TestInstanceBase(t *testing.T) {
	binPath, err := os.Executable()
	require.NoErrorf(t, err, `Can't get the path to the executable. Error: "%v".`, err)

	consoleSock := filepath.Join(filepath.Dir(binPath), "test.sock")
	binaryPort := filepath.Join(filepath.Dir(binPath), "testbin.sock")

	logger := ttlog.NewCustomLogger(io.Discard, "", 0)
	inst := startTestInstance(t, context.Background(), "dumb_test_app", consoleSock,
		binaryPort, logger)
	t.Cleanup(func() { cleanupTestInstance(t, inst, consoleSock) })

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "unix", consoleSock)
	require.NoErrorf(t, err, `Can't connect to console socket. Error: "%v".`, err)

	_ = conn.Close()
}

func TestInstanceLogger(t *testing.T) {
	assert := assert.New(t)

	reader, writer := io.Pipe()
	logger := ttlog.NewCustomLogger(writer, "", 0)
	consoleSock := ""
	inst := startTestInstance(t, context.Background(), "log_check_test_app", consoleSock, "",
		logger)
	t.Cleanup(func() {
		// Nobody reads the log any more: closing the reader first lets the
		// output of tarantool fail instead of blocking its exit.
		_ = reader.Close()

		cleanupTestInstance(t, inst, consoleSock)

		_ = writer.Close()
	})

	msg := "Check Log.\n"
	msgLen := int64(len(msg))
	buf := bytes.NewBufferString("")
	_, err := io.CopyN(buf, reader, msgLen)
	assert.Equal(msg, buf.String(), "The message in the log is different from what was expected.")
	assert.NoErrorf(err, `Can't read log output. Error: "%v".`, err)
}

func Test_shortenSocketPath(t *testing.T) {
	type args struct {
		socketPath string
		basePath   string
	}

	maxSocketPathLen := maxSocketPathLinux
	if runtime.GOOS == "darwin" {
		maxSocketPathLen = maxSocketPathMac
	}

	dirLen := maxSocketPathLen - len("/tarantool.control") - 1
	maxSocketPath := "/" + strings.Repeat("a", dirLen) + "/tarantool.control"
	require.Len(t, maxSocketPath, maxSocketPathLen)

	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "root base path",
			args: args{
				socketPath: "/var/run/app/inst/tarantool.control",
				basePath:   "/",
			},
			want:    "/var/run/app/inst/tarantool.control",
			wantErr: false,
		},
		{
			name: "/var/run base path",
			args: args{
				socketPath: "/var/run/app/inst/tarantool.control",
				basePath:   "/var/run",
			},
			want:    "/var/run/app/inst/tarantool.control",
			wantErr: false,
		},
		{
			name: "long socket path",
			args: args{
				socketPath: "/" + strings.Repeat("aaaaaaaaaa/", 11) + "/tarantool.control",
				basePath:   "/" + strings.Repeat("aaaaaaaaaa/", 10) + "/",
			},
			want:    "aaaaaaaaaa/tarantool.control",
			wantErr: false,
		},
		{
			name: "long socket path, one level up",
			args: args{
				socketPath: "/" + strings.Repeat("aaaaaaaaaa/", 11) + "/tarantool.control",
				basePath:   "/" + strings.Repeat("aaaaaaaaaa/", 10) + "/bbb/",
			},
			want:    "../aaaaaaaaaa/tarantool.control",
			wantErr: false,
		},
		{
			name: "long socket path, no way to make it shorter",
			args: args{
				socketPath: "/" + strings.Repeat("aaaaaaaaaa/", 11) + "/tarantool.control",
				basePath:   "",
			},
			want:    "../aaaaaaaaaa/tarantool.control",
			wantErr: true,
		},
		{
			name: "max socket path",
			args: args{
				socketPath: maxSocketPath,
				basePath:   "",
			},
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := shortenSocketPath(tt.args.socketPath, tt.args.basePath)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestInstanceLogs(t *testing.T) {
	binPath, err := os.Executable()
	require.NoError(t, err)

	consoleSock := filepath.Join(filepath.Dir(binPath), "test.sock")
	binaryPort := filepath.Join(filepath.Dir(binPath), "testbin.sock")

	app := "dumb_test_app"
	// Need absolute path to the script, because working dir is changed on start.
	appPath, err := filepath.Abs(filepath.Join(instTestAppDir, app+".lua"))
	require.NoError(t, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	require.NoError(t, err)

	instTestDataDir := t.TempDir()
	binDir := filepath.Dir(binPath)

	t.Setenv("started_flag_file", filepath.Join(binDir, app))

	spec, err := scriptSpec(tarantoolBin, &InstanceCtx{
		AppDir:         binDir,
		InstanceScript: appPath,
		ConsoleSocket:  consoleSock,
		WalDir:         instTestDataDir,
		VinylDir:       instTestDataDir,
		MemtxDir:       instTestDataDir,
		LogDir:         instTestDataDir,
		BinaryPort:     binaryPort,
	}, specOptions{integrity: nil, stdout: os.Stdout, stderr: os.Stderr})
	require.NoError(t, err)

	defer func() {
		_ = os.Remove(os.Getenv("started_flag_file"))
	}()

	inst := startSpec(t, context.Background(), spec)
	t.Cleanup(func() { cleanupTestInstance(t, inst, consoleSock) })

	require.NotZero(t, waitForFile(os.Getenv("started_flag_file")), "Instance is not started")
	assert.True(t, inst.alive())

	assert.FileExists(t, filepath.Join(filepath.Dir(binPath), "test.sock"))
	assert.FileExists(t, filepath.Join(filepath.Dir(binPath), "testbin.sock"))
}

func TestInstanceStopByContext(t *testing.T) {
	tmpdir := t.TempDir()

	consoleSock := filepath.Join(tmpdir, "test.sock")
	binaryPort := filepath.Join(tmpdir, "testbin.sock")

	logger := ttlog.NewCustomLogger(io.Discard, "", 0)
	ctx, cancel := context.WithCancel(context.Background())
	inst := startTestInstance(t, ctx, "dumb_test_app", consoleSock, binaryPort, logger)
	t.Cleanup(func() { cleanupTestInstance(t, inst, consoleSock) })

	cancel()
	require.ErrorIs(t, inst.wait(), context.Canceled)
	assert.True(t, inst.cmd.ProcessState.Success())
}
