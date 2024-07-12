package running

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/lib/integrity"
)

const (
	wdTestAppDir         = "./test_app"
	wdTestAppName        = "dumb_test_app"
	wdTestRestartTimeout = 100 * time.Millisecond
	wdTestStopTimeout    = 2 * time.Second
)

// providerTestImpl is an implementation of the Watchdog provider used for tests.
type providerTestImpl struct {
	// tarantool is the tarantool binary in use.
	tarantool string
	// appPath is the path to the application.
	appPath string
	// logger describes the logger used by Watchdog.
	logger ttlog.Logger
	// dataDir used by an Instance.
	dataDir string
	// restartable indicates the need to restart the instance in case of a crash.
	restartable bool
	t           *testing.T
}

// createInstance reads config and creates an Instance.
func (provider *providerTestImpl) CreateInstance(logger ttlog.Logger) (Instance, error) {
	return newScriptInstance(provider.tarantool, InstanceCtx{
		InstanceScript: provider.appPath,
		AppDir:         provider.t.TempDir(),
	},
		StdLoggerOpt(logger))
}

// UpdateLogger updates the logger settings or creates a new logger, if passed nil.
func (provider *providerTestImpl) UpdateLogger(logger ttlog.Logger) (ttlog.Logger, error) {
	return logger, nil
}

// IsRestartable checks if the instance should be restarted in case of crash.
func (provider *providerTestImpl) IsRestartable() (bool, error) {
	return provider.restartable, nil
}

// createTestWatchdog creates an instance and a watchdog for the test.
func createTestWatchdog(t *testing.T, restartable bool) *Watchdog {
	assert := assert.New(t)

	dataDir := t.TempDir()

	// Need absolute path to the script, because working dir is changed on start.
	appPath, err := filepath.Abs(filepath.Join(wdTestAppDir, wdTestAppName+".lua"))
	assert.Nilf(err, `Unknown application: "%v". Error: "%v".`, appPath, err)

	tarantoolBin, err := exec.LookPath("tarantool")
	assert.Nilf(err, `Can't find a tarantool binary. Error: "%v".`, err)

	logger := ttlog.NewCustomLogger(io.Discard, "", 0)

	provider := providerTestImpl{tarantool: tarantoolBin, appPath: appPath, logger: logger,
		dataDir: dataDir, restartable: restartable, t: t}
	testPreAction := func() error { return nil }
	wd := NewWatchdog(restartable, wdTestRestartTimeout, logger, &provider, testPreAction,
		integrity.IntegrityCtx{
			Repository: &mockRepository{},
		}, 0)

	return wd
}

// killAndCheckRestart kills the instance by signal and checks if a
// new instance has been started.
func killAndCheckRestart(t *testing.T, wd *Watchdog, signal syscall.Signal) {
	// Remove the file. It must be created again by the restarted instance.
	os.Remove(os.Getenv("started_flag_file"))
	wd.instance.SendSignal(signal)
	// No need to check for PID changes. If the file is created again, new process is started.
	require.NotZero(t, waitForFile(os.Getenv("started_flag_file")), "Instance is not started")
	assert.True(t, wd.instance.IsAlive(), "Instance doesn't restart.")
}

// cleanupWatchdog kills the instance and stops the watchdog.
func cleanupWatchdog(wd *Watchdog) {
	provider := wd.provider.(*providerTestImpl)
	provider.restartable = false
	if wd.instance != nil && wd.instance.IsAlive() {
		wd.instance.Stop(5 * time.Second)
	}
	os.Remove(os.Getenv("started_flag_file"))
}

func TestWatchdogBase(t *testing.T) {
	assert := assert.New(t)

	binPath, err := os.Executable()
	require.NoErrorf(t, err, `Can't get the path to the executable. Error: "%v".`, err)
	os.Setenv("started_flag_file", filepath.Join(filepath.Dir(binPath), t.Name()))

	wd := createTestWatchdog(t, true)
	t.Cleanup(func() { cleanupWatchdog(wd) })

	wdDoneChan := make(chan bool, 1)
	go func() {
		wd.Start()
		wdDoneChan <- true
	}()

	require.NotZero(t, waitForFile(os.Getenv("started_flag_file")), "Instance is not started")

	alive := wd.instance.IsAlive()
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

	binPath, err := os.Executable()
	require.NoErrorf(t, err, `Can't get the path to the executable. Error: "%v".`, err)
	os.Setenv("started_flag_file", filepath.Join(filepath.Dir(binPath), t.Name()))

	wd := createTestWatchdog(t, false)
	t.Cleanup(func() { cleanupWatchdog(wd) })

	wdDoneChan := make(chan bool, 1)
	go func() {
		wd.Start()
		wdDoneChan <- true
	}()
	require.NotZero(t, waitForFile(os.Getenv("started_flag_file")), "Instance is not started")

	alive := wd.instance.IsAlive()
	assert.True(alive, "Can't start the instance under watchdog.")

	wd.instance.SendSignal(syscall.SIGINT)

	// The watchdog should stop because the instance was killed and
	// the "Restartable" flag is false.
	select {
	case <-time.After(wdTestStopTimeout):
		assert.Fail("Can't stop the watchdog.")
	case <-wdDoneChan:
	}
}
