package running

import (
	"os"
	"os/exec"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	instTestAppDir  = "./test_app"
	instTestAppName = "dumb_test_app"
)

// startTestInstance starts instance for the test.
func startTestInstance(t *testing.T) *Instance {
	assert := assert.New(t)

	appPath := path.Join(instTestAppDir, instTestAppName+".lua")
	_, err := os.Stat(appPath)
	assert.Nilf(err, `Unknown application: "%v". Error: "%v".`, appPath, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	assert.Nilf(err, `Can't find a tarantool binary. Error: "%v".`, err)

	inst, err := NewInstance(tarantoolBin, appPath, os.Environ())
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
}

func TestInstanceBase(t *testing.T) {
	assert := assert.New(t)

	inst := startTestInstance(t)
	t.Cleanup(func() { cleanupTestInstance(inst) })

	err := inst.Stop(30 * time.Second)
	assert.Nilf(err, `Can't stop the instance. Error: "%v".`, err)
}
