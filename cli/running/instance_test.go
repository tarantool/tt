package running

import (
	"bytes"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/ttlog"
)

const (
	instTestAppDir = "./test_app"
)

// startTestInstance starts instance for the test.
func startTestInstance(t *testing.T, app string, consoleSock string,
	logger *ttlog.Logger) *Instance {
	assert := assert.New(t)

	instTestDataDir, err := ioutil.TempDir("", "tarantool_tt_")
	t.Cleanup(func() { cleanupTempDir(instTestDataDir) })
	assert.Nilf(err, `Can't create dataDir: "%v". Error: "%v".`, instTestDataDir, err)

	appPath := path.Join(instTestAppDir, app+".lua")
	_, err = os.Stat(appPath)
	assert.Nilf(err, `Unknown application: "%v". Error: "%v".`, appPath, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	assert.Nilf(err, `Can't find a tarantool binary. Error: "%v".`, err)

	inst, err := NewInstance(tarantoolBin, appPath, "", "", consoleSock, os.Environ(),
		logger, instTestDataDir)
	assert.Nilf(err, `Can't create an instance. Error: "%v".`, err)

	err = inst.Start()
	assert.Nilf(err, `Can't start the instance. Error: "%v".`, err)

	waitProcessStart()
	alive := inst.IsAlive()
	assert.True(alive, "Can't start the instance.")

	return inst
}

// cleanupTestInstance sends a SIGKILL signal to test
// Instance that remain alive after the test done.
func cleanupTestInstance(inst *Instance) {
	if inst.IsAlive() {
		inst.Cmd.Process.Kill()
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

	logger := ttlog.NewCustomLogger(io.Discard, "", 0)
	inst := startTestInstance(t, "dumb_test_app", consoleSock, logger)
	t.Cleanup(func() { cleanupTestInstance(inst) })

	conn, err := net.Dial("unix", consoleSock)
	assert.Nilf(err, `Can't connect to console socket. Error: "%v".`, err)
	conn.Close()

	err = inst.Stop(30 * time.Second)
	assert.Nilf(err, `Can't stop the instance. Error: "%v".`, err)
}

func TestInstanceLogger(t *testing.T) {
	assert := assert.New(t)

	reader, writer := io.Pipe()
	defer writer.Close()
	defer reader.Close()
	logger := ttlog.NewCustomLogger(writer, "", 0)
	consoleSock := ""
	inst := startTestInstance(t, "log_check_test_app", consoleSock, logger)
	t.Cleanup(func() { cleanupTestInstance(inst) })

	msg := "Check Log.\n"
	msgLen := int64(len(msg))
	buf := bytes.NewBufferString("")
	_, err := io.CopyN(buf, reader, msgLen)
	assert.Equal(msg, buf.String(), "The message in the log is different from what was expected.")
	assert.Nilf(err, `Can't read log output. Error: "%v".`, err)

	err = inst.Stop(30 * time.Second)
	assert.Nilf(err, `Can't stop the instance. Error: "%v".`, err)
}
