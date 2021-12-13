package running

import (
	"os"
	"os/exec"
	"path"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const (
	wdTestAppDir         = "./test_app"
	wdTestAppName        = "dumb_test_app"
	wdTestRestartTimeout = 100 * time.Millisecond
	wdTestStopTimeout    = 2 * time.Second
)

// createTestWatchdog creates an instance and a watchdog for the test.
func createTestWatchdog(t *testing.T, restartable bool) *Watchdog {
	assert := assert.New(t)

	appPath := path.Join(wdTestAppDir, wdTestAppName+".lua")
	_, err := os.Stat(appPath)
	assert.Nilf(err, `Unknown application: "%v". Error: "%v".`, appPath, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	assert.Nilf(err, `Can't find a tarantool binary. Error: "%v".`, err)

	inst, err := NewInstance(tarantoolBin, appPath, "", os.Environ())
	assert.Nilf(err, `Can't create an instance. Error: "%v".`, err)

	wd := NewWatchdog(inst, restartable, wdTestRestartTimeout)

	return wd
}

// killAndCheckRestart kills the instance by signal and checks if a
// new instance has been started.
func killAndCheckRestart(t *testing.T, wd *Watchdog, signal syscall.Signal) {
	assert := assert.New(t)

	oldPid := wd.Instance.Cmd.Process.Pid
	wd.Instance.SendSignal(signal)
	time.Sleep(wdTestRestartTimeout * 2)

	assert.True(wd.Instance.IsAlive(), "Instance doesn't restart.")
	assert.NotEqual(oldPid, wd.Instance.Cmd.Process.Pid, "The old Instance is alive.")
}

// cleanupWatchdog kills the instance and stops the watchdog.
func cleanupWatchdog(wd *Watchdog) {
	wd.restartable = false
	if wd.Instance != nil && wd.Instance.IsAlive() {
		syscall.Kill(wd.Instance.Cmd.Process.Pid, syscall.SIGKILL)
	}
}

func TestWatchdogBase(t *testing.T) {
	assert := assert.New(t)

	wd := createTestWatchdog(t, true)
	t.Cleanup(func() { cleanupWatchdog(wd) })

	wdDoneChan := make(chan bool, 1)
	go func() {
		wd.Start()
		wdDoneChan <- true
	}()
	waitProcessStart()

	alive := wd.Instance.IsAlive()
	assert.True(alive, "Can't start the instance under watchdog.")

	killAndCheckRestart(t, wd, syscall.SIGINT)
	killAndCheckRestart(t, wd, syscall.SIGKILL)

	// Let's try to stop the watchdog by a signal.
	syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	select {
	case <-time.After(wdTestStopTimeout):
		assert.Fail("Can't stop the watchdog.")
	case <-wdDoneChan:
	}
}

func TestWatchdogNotRestartable(t *testing.T) {
	assert := assert.New(t)

	wd := createTestWatchdog(t, false)
	t.Cleanup(func() { cleanupWatchdog(wd) })

	wdDoneChan := make(chan bool, 1)
	go func() {
		wd.Start()
		wdDoneChan <- true
	}()
	waitProcessStart()

	alive := wd.Instance.IsAlive()
	assert.True(alive, "Can't start the instance under watchdog.")

	wd.Instance.SendSignal(syscall.SIGINT)

	// The watchdog should stop because the instance was killed and
	// the "Restartable" flag is false.
	select {
	case <-time.After(wdTestStopTimeout):
		assert.Fail("Can't stop the watchdog.")
	case <-wdDoneChan:
	}
}
