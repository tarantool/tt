package running

import (
	"bytes"
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
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/lib/integrity"
)

const (
	instTestAppDir = "./test_app"
)

// startTestInstance starts instance for the test.
func startTestInstance(t *testing.T, app string, consoleSock string, binaryPort string,
	logger ttlog.Logger) *scriptInstance {
	assert := assert.New(t)

	// Need absolute path to the script, because working dir is changed on start.
	appPath, err := filepath.Abs(filepath.Join(instTestAppDir, app+".lua"))
	assert.Nilf(err, `Unknown application: "%v". Error: "%v".`, appPath, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	assert.Nilf(err, `Can't find a tarantool binary. Error: "%v".`, err)

	instTestDataDir := t.TempDir()
	binPath, err := os.Executable()
	require.NoError(t, err)
	binDir := filepath.Dir(binPath)
	inst, err := newScriptInstance(tarantoolBin, InstanceCtx{
		AppDir:         binDir,
		InstanceScript: appPath,
		ConsoleSocket:  consoleSock,
		WalDir:         instTestDataDir,
		VinylDir:       instTestDataDir,
		MemtxDir:       instTestDataDir,
		BinaryPort:     binaryPort,
	},
		logger, integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, false)
	assert.Nilf(err, `Can't create an instance. Error: "%v".`, err)

	require.NoErrorf(t, err, `Can't get the path to the executable. Error: "%v".`, err)
	os.Setenv("started_flag_file", filepath.Join(binDir, app))
	defer os.Remove(os.Getenv("started_flag_file"))
	err = inst.Start()
	assert.Nilf(err, `Can't start the instance. Error: "%v".`, err)

	require.NotZero(t, waitForFile(os.Getenv("started_flag_file")), "Instance is not started")
	alive := inst.IsAlive()
	assert.True(alive, "Can't start the instance.")

	return inst
}

// cleanupTestInstance sends a SIGKILL signal to test
// Instance that remain alive after the test done.
func cleanupTestInstance(t *testing.T, inst *scriptInstance) {
	if inst.IsAlive() {
		err := inst.Stop(stopTimeout)
		assert.NoError(t, err)
	}
	if _, err := os.Stat(inst.consoleSocket); err == nil {
		os.Remove(inst.consoleSocket)
	}
}

func TestInstanceBase(t *testing.T) {
	assert := assert.New(t)

	binPath, err := os.Executable()
	assert.Nilf(err, `Can't get the path to the executable. Error: "%v".`, err)
	consoleSock := filepath.Join(filepath.Dir(binPath), "test.sock")
	binaryPort := filepath.Join(filepath.Dir(binPath), "testbin.sock")

	logger := ttlog.NewCustomLogger(io.Discard, "", 0)
	inst := startTestInstance(t, "dumb_test_app", consoleSock, binaryPort, logger)
	t.Cleanup(func() { cleanupTestInstance(t, inst) })

	conn, err := net.Dial("unix", consoleSock)
	assert.Nilf(err, `Can't connect to console socket. Error: "%v".`, err)
	conn.Close()
}

func TestInstanceLogger(t *testing.T) {
	assert := assert.New(t)

	reader, writer := io.Pipe()
	defer writer.Close()
	defer reader.Close()
	logger := ttlog.NewCustomLogger(writer, "", 0)
	consoleSock := ""
	inst := startTestInstance(t, "log_check_test_app", consoleSock, "", logger)
	t.Cleanup(func() { cleanupTestInstance(t, inst) })

	msg := "Check Log.\n"
	msgLen := int64(len(msg))
	buf := bytes.NewBufferString("")
	_, err := io.CopyN(buf, reader, msgLen)
	assert.Equal(msg, buf.String(), "The message in the log is different from what was expected.")
	assert.Nilf(err, `Can't read log output. Error: "%v".`, err)
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
	require.Equal(t, maxSocketPathLen, len(maxSocketPath))

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
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestInstanceLogs(t *testing.T) {
	binPath, err := os.Executable()
	assert.NoError(t, err)
	consoleSock := filepath.Join(filepath.Dir(binPath), "test.sock")
	binaryPort := filepath.Join(filepath.Dir(binPath), "testbin.sock")

	app := "dumb_test_app"
	// Need absolute path to the script, because working dir is changed on start.
	appPath, err := filepath.Abs(filepath.Join(instTestAppDir, app+".lua"))
	assert.NoError(t, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	require.NoError(t, err)
	logger := ttlog.NewCustomLogger(os.Stdout, "", 0)

	instTestDataDir := t.TempDir()
	binDir := filepath.Dir(binPath)
	inst, err := newScriptInstance(tarantoolBin, InstanceCtx{
		AppDir:         binDir,
		InstanceScript: appPath,
		ConsoleSocket:  consoleSock,
		WalDir:         instTestDataDir,
		VinylDir:       instTestDataDir,
		MemtxDir:       instTestDataDir,
		LogDir:         instTestDataDir,
		BinaryPort:     binaryPort,
	},
		logger, integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, false)
	require.NoError(t, err)

	t.Cleanup(func() { cleanupTestInstance(t, inst) })

	require.NoErrorf(t, err, `Can't get the path to the executable. Error: "%v".`, err)
	os.Setenv("started_flag_file", filepath.Join(binDir, app))
	defer os.Remove(os.Getenv("started_flag_file"))
	err = inst.Start()
	require.NoError(t, err)

	require.NotZero(t, waitForFile(os.Getenv("started_flag_file")), "Instance is not started")
	alive := inst.IsAlive()
	assert.True(t, alive)

	assert.FileExists(t, filepath.Join(filepath.Dir(binPath), "test.sock"))
	assert.FileExists(t, filepath.Join(filepath.Dir(binPath), "testbin.sock"))
}
