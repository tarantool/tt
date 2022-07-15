package running

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/ttlog"
)

// startTestRun starts instance for the test.
func startTestRun(t *testing.T, app string, consoleSock string,
	logger *ttlog.Logger, flags *RunFlags) *Instance {

	assert := assert.New(t)
	appPath := ""
	if app != "" {
		appPath := path.Join(instTestAppDir, app+".lua")
		_, err := os.Stat(appPath)
		assert.Nilf(err, `Unknown application: "%v". Error: "%v".`, appPath, err)
	}

	tarantoolBin, err := exec.LookPath("tarantool")
	assert.Nilf(err, `Can't find a tarantool binary. Error: "%v".`, err)
	inst := &Instance{tarantoolPath: tarantoolBin, appPath: appPath,
		env: os.Environ()}

	assert.Nilf(err, `Can't create an instance. Error: "%v".`, err)

	err = inst.Run(flags)
	assert.Nilf(err, `Can't start the instance. Error: "%v".`, err)

	return inst
}

// cleanupTestRun sends a SIGKILL signal to test
// Instance that remain alive after the test done.
func cleanupTestRun(inst *Instance) {
	if inst.IsAlive() {
		inst.Cmd.Process.Kill()
	}
	if _, err := os.Stat(inst.consoleSocket); err == nil {
		os.Remove(inst.consoleSocket)
	}
}
func TestEvalflag(t *testing.T) {
	assert := assert.New(t)

	flags := &RunFlags{RunEval: "print('123')", RunLib: "",
		RunInteractive: false, RunStdin: "", RunVersion: false}
	old := os.Stdout
	readPipe, writePipe, _ := os.Pipe()
	os.Stdout = writePipe
	startTestRun(t, "", "", nil, flags)

	writePipe.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, readPipe)
	assert.Equal("123\n", buf.String(),
		"The message in the log is different from what was expected.")
}

func TestStdinflag(t *testing.T) {
	assert := assert.New(t)

	flags := &RunFlags{RunEval: "", RunLib: "",
		RunInteractive: false, RunStdin: "print('123')", RunVersion: false}
	old := os.Stdout
	readPipe, writePipe, _ := os.Pipe()
	os.Stdout = writePipe
	startTestRun(t, "", "", nil, flags)

	writePipe.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, readPipe)
	assert.Equal("123\n", buf.String(),
		"The message in the log is different from what was expected.")
}

func TestVersionflag(t *testing.T) {
	assert := assert.New(t)

	flags := &RunFlags{RunEval: "", RunLib: "",
		RunInteractive: false, RunStdin: "", RunVersion: true}
	old := os.Stdout
	readPipe, writePipe, _ := os.Pipe()
	os.Stdout = writePipe
	startTestRun(t, "", "", nil, flags)

	writePipe.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, readPipe)
	assert.Contains(buf.String(), "Tarantool",
		"The message in the log is different from what was expected.")
}

func TestInteractiveflag(t *testing.T) {
	assert := assert.New(t)

	flags := &RunFlags{RunEval: "", RunLib: "",
		RunInteractive: true, RunStdin: "", RunVersion: false}
	old := os.Stdout
	readPipe, writePipe, _ := os.Pipe()
	os.Stdout = writePipe
	inst := startTestRun(t, "dumb_test_run", "", nil, flags)
	writePipe.Close()
	os.Stdout = old

	var buf bytes.Buffer

	io.Copy(&buf, readPipe)
	assert.Equal("tarantool> ", buf.String(),
		"The message in the log is different from what was expected.")
	inst.Stop(50 * time.Millisecond)
}

func TestLibflag(t *testing.T) {
	assert := assert.New(t)

	flags := &RunFlags{RunEval: "print('123')", RunLib: "os",
		RunInteractive: false, RunStdin: "", RunVersion: false}
	old := os.Stdout
	readPipe, writePipe, _ := os.Pipe()
	os.Stdout = writePipe
	inst := startTestRun(t, "dumb_test_run", "", nil, flags)
	writePipe.Close()
	os.Stdout = old

	var buf bytes.Buffer

	io.Copy(&buf, readPipe)
	assert.Equal("123\n", buf.String(),
		"The message in the log is different from what was expected.")
	inst.Stop(50 * time.Millisecond)
}
