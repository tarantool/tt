package tcm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The watchdog of a test runs in a process of its own, the test binary run
// with helperEnv set, so that it receives real signals. It runs fakeTCM.
const (
	helperEnv      = "TCM_WATCHDOG_TEST_HELPER"
	executableEnv  = "TCM_WATCHDOG_TEST_EXECUTABLE"
	stopTimeoutEnv = "TCM_WATCHDOG_TEST_STOP_TIMEOUT"
	checkEnv       = "TCM_WATCHDOG_TEST_CHECK"
	// dirEnv and modeEnv reach fakeTCM through the watchdog.
	dirEnv  = "TCM_TEST_DIR"
	modeEnv = "TCM_TEST_MODE"
)

// Modes of fakeTCM.
const (
	// modeServe records the stop signal it gets and exits.
	modeServe = "serve"
	// modeStubborn ignores the stop signals.
	modeStubborn = "stubborn"
	// modeExitOnce exits with 3 the first time it runs in a directory, and
	// serves after that.
	modeExitOnce = "exit-once"
)

// fakeTCM stands in for TCM. Every run writes a started.<pid> file, and a
// serving one appends each stop signal it gets to signals.<pid> and exits.
const fakeTCM = `#!/bin/sh
dir="$TCM_TEST_DIR"
record() { echo "$1" >> "$dir/signals.$$"; exit 0; }
if [ "$TCM_TEST_MODE" = stubborn ]; then
	trap '' TERM INT HUP QUIT
else
	trap 'record TERM' TERM
	trap 'record INT' INT
	trap 'record HUP' HUP
	trap 'record QUIT' QUIT
fi
if [ "$TCM_TEST_MODE" = exit-once ] && [ ! -e "$dir/exited" ]; then
	: > "$dir/exited"
	: > "$dir/started.$$"
	exit 3
fi
: > "$dir/started.$$"
while :; do sleep 0.05; done
`

const (
	watchdogPid  = "watchdog.pid"
	tcmPid       = "tcm.pid"
	restartDelay = 100 * time.Millisecond
	waitTimeout  = 10 * time.Second
	pollInterval = 10 * time.Millisecond
)

var errCheck = errors.New("a checked file changed")

// TestHelperWatchdog is the watchdog of the other tests; it runs only in the
// process they start.
func TestHelperWatchdog(t *testing.T) {
	if os.Getenv(helperEnv) == "" {
		t.Skip("runs only as the watchdog of the other tests")
	}

	stopTimeout, err := time.ParseDuration(os.Getenv(stopTimeoutEnv))
	if err != nil {
		panic(err)
	}

	opts := WatchdogOpts{
		Executable:   os.Getenv(executableEnv),
		PidFile:      watchdogPid,
		ChildPidFile: tcmPid,
		RestartDelay: restartDelay,
		StopTimeout:  stopTimeout,
		CheckPeriod:  0,
		Check:        nil,
	}

	if os.Getenv(checkEnv) != "" {
		opts.CheckPeriod = 50 * time.Millisecond
		// The check fails once fakeTCM is under way.
		opts.Check = func(context.Context) error {
			started, err := filepath.Glob(filepath.Join(os.Getenv(dirEnv), "started.*"))
			if err != nil || len(started) == 0 {
				return err
			}

			return errCheck
		}
	}

	err = RunWatchdog(opts)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}

	os.Exit(0)
}

// watchdog is a watchdog the test started.
type watchdog struct {
	t    *testing.T
	dir  string
	cmd  *exec.Cmd
	done chan struct{}
	// out is the file with the output of the watchdog.
	out string
}

// config is what a test runs.
type config struct {
	mode        string
	stopTimeout time.Duration
	failCheck   bool
}

// startWatchdog starts a watchdog in dir, which runs fakeTCM in cfg.mode.
func startWatchdog(t *testing.T, dir string, cfg config) *watchdog {
	t.Helper()

	exe, err := os.Executable()
	require.NoError(t, err)

	// A watchdog already running in dir may be starting the script.
	script := filepath.Join(dir, "tcm")

	_, err = os.Stat(script)
	if err != nil {
		require.NoError(t, os.WriteFile(script, []byte(fakeTCM), 0o700))
	}

	out, err := os.CreateTemp(dir, "watchdog-*.log")
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestHelperWatchdog$")

	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = out

	cmd.Env = append(os.Environ(),
		helperEnv+"=1",
		executableEnv+"="+script,
		stopTimeoutEnv+"="+cfg.stopTimeout.String(),
		dirEnv+"="+dir,
		modeEnv+"="+cfg.mode,
	)

	if cfg.failCheck {
		cmd.Env = append(cmd.Env, checkEnv+"=1")
	}

	require.NoError(t, cmd.Start())
	require.NoError(t, out.Close())

	dog := &watchdog{t: t, dir: dir, cmd: cmd, done: make(chan struct{}), out: out.Name()}

	go func() {
		_ = cmd.Wait()

		close(dog.done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-dog.done

		for _, pid := range dog.started() {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})

	return dog
}

// started returns the pids of the runs of fakeTCM so far.
func (dog *watchdog) started() []int {
	matches, err := filepath.Glob(filepath.Join(dog.dir, "started.*"))
	require.NoError(dog.t, err)

	pids := make([]int, 0, len(matches))

	for _, match := range matches {
		pid, err := strconv.Atoi(strings.TrimPrefix(filepath.Ext(match), "."))
		require.NoError(dog.t, err)

		pids = append(pids, pid)
	}

	return pids
}

// readPid returns the pid in the pid file name, 0 if there is none.
func (dog *watchdog) readPid(name string) int {
	data, err := os.ReadFile(filepath.Join(dog.dir, name))
	if err != nil {
		return 0
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		return 0
	}

	return pid
}

// waitRunning waits for that many runs of fakeTCM and for the pid file to name a
// live one of them, and returns its pid.
func (dog *watchdog) waitRunning(runs int) int {
	dog.t.Helper()

	var pid int

	running := assert.Eventually(dog.t, func() bool {
		started := dog.started()

		pid = dog.readPid(tcmPid)

		return len(started) >= runs && slices.Contains(started, pid) &&
			syscall.Kill(pid, 0) == nil
	}, waitTimeout, pollInterval)
	if !running {
		require.FailNow(dog.t, "fakeTCM did not start", dog.output())
	}

	return pid
}

// wait waits for the watchdog to exit and returns its exit code.
func (dog *watchdog) wait() int {
	dog.t.Helper()

	select {
	case <-dog.done:
	case <-time.After(waitTimeout):
		require.FailNow(dog.t, "the watchdog did not exit", dog.output())
	}

	return dog.cmd.ProcessState.ExitCode()
}

// output returns what the watchdog has written.
func (dog *watchdog) output() string {
	data, err := os.ReadFile(dog.out)
	if err != nil {
		return err.Error()
	}

	return string(data)
}

// assertGone asserts that the watchdog left no pid file and no fakeTCM.
func (dog *watchdog) assertGone() {
	dog.t.Helper()

	assert.NoFileExists(dog.t, filepath.Join(dog.dir, watchdogPid))
	assert.NoFileExists(dog.t, filepath.Join(dog.dir, tcmPid))

	for _, pid := range dog.started() {
		assert.ErrorIs(dog.t, syscall.Kill(pid, 0), syscall.ESRCH, "fakeTCM %d is alive", pid)
	}
}

// TestWatchdogStopSignals pins the stop of TCM: each stop signal the watchdog
// gets stops TCM, which runs in a process group of its own, with SIGTERM, and
// the watchdog removes both pid files and exits.
func TestWatchdogStopSignals(t *testing.T) {
	signals := map[syscall.Signal]string{
		syscall.SIGINT:  "INT",
		syscall.SIGTERM: "TERM",
		syscall.SIGHUP:  "HUP",
		syscall.SIGQUIT: "QUIT",
	}

	for sig, name := range signals {
		t.Run(name, func(t *testing.T) {
			dog := startWatchdog(t, t.TempDir(), config{
				mode:        modeServe,
				stopTimeout: waitTimeout,
				failCheck:   false,
			})

			pid := dog.waitRunning(1)

			assert.Equal(t, dog.cmd.Process.Pid, dog.readPid(watchdogPid))

			pgid, err := syscall.Getpgid(pid)
			require.NoError(t, err)
			assert.Equal(t, pid, pgid, "fakeTCM does not lead a process group")

			require.NoError(t, dog.cmd.Process.Signal(sig))
			require.Zero(t, dog.wait(), dog.output())

			signalsGot, err := os.ReadFile(filepath.Join(dog.dir, "signals."+strconv.Itoa(pid)))
			require.NoError(t, err, dog.output())
			assert.Equal(t, "TERM\n", string(signalsGot))

			dog.assertGone()
			assert.Contains(t, dog.output(), "Process started successfully")
			assert.Contains(t, dog.output(), "Watchdog stopped.")
		})
	}
}

// TestWatchdogRestartsTCM pins that TCM runs again after it exits on its own,
// with its pid file naming the new process.
func TestWatchdogRestartsTCM(t *testing.T) {
	dog := startWatchdog(t, t.TempDir(), config{
		mode:        modeExitOnce,
		stopTimeout: waitTimeout,
		failCheck:   false,
	})

	pid := dog.waitRunning(2)

	require.Len(t, dog.started(), 2)
	assert.Contains(t, dog.output(), "Waiting "+restartDelay.String()+" before restart...")

	require.NoError(t, dog.cmd.Process.Signal(syscall.SIGTERM))
	require.Zero(t, dog.wait(), dog.output())

	_, err := os.Stat(filepath.Join(dog.dir, "signals."+strconv.Itoa(pid)))
	require.NoError(t, err, "the second fakeTCM was not stopped")
	dog.assertGone()
}

// TestWatchdogRefusesSecond pins that a second watchdog in the same directory
// is refused while the first runs, and leaves the files of the first alone.
func TestWatchdogRefusesSecond(t *testing.T) {
	dir := t.TempDir()
	cfg := config{mode: modeServe, stopTimeout: waitTimeout, failCheck: false}

	first := startWatchdog(t, dir, cfg)
	pid := first.waitRunning(1)

	second := startWatchdog(t, dir, cfg)
	require.Equal(t, 1, second.wait(), second.output())
	assert.Contains(t, second.output(), "the pid file belongs to a running process")

	assert.Equal(t, first.cmd.Process.Pid, first.readPid(watchdogPid))
	assert.Equal(t, pid, first.readPid(tcmPid))
	assert.Len(t, first.started(), 1, "the second watchdog started TCM")

	require.NoError(t, first.cmd.Process.Signal(syscall.SIGTERM))
	require.Zero(t, first.wait(), first.output())
	first.assertGone()
}

// TestWatchdogKillsStubbornTCM pins that TCM still running a stop timeout
// after SIGTERM is killed, with its process group.
func TestWatchdogKillsStubbornTCM(t *testing.T) {
	dog := startWatchdog(t, t.TempDir(), config{
		mode:        modeStubborn,
		stopTimeout: 300 * time.Millisecond,
		failCheck:   false,
	})

	dog.waitRunning(1)

	require.NoError(t, dog.cmd.Process.Signal(syscall.SIGTERM))
	require.Zero(t, dog.wait(), dog.output())

	dog.assertGone()
	assert.Contains(t, dog.output(), "killed: stop timeout")
}

// TestWatchdogIntegrityHardStop pins that a failed periodic check kills TCM
// and ends the watchdog with the failure, although TCM restarts after any
// other exit.
func TestWatchdogIntegrityHardStop(t *testing.T) {
	dog := startWatchdog(t, t.TempDir(), config{
		mode:        modeServe,
		stopTimeout: waitTimeout,
		failCheck:   true,
	})

	require.Equal(t, 1, dog.wait(), dog.output())
	assert.Contains(t, dog.output(), errCheck.Error())
	assert.Contains(t, dog.output(), "killed: check failed")
	assert.Len(t, dog.started(), 1, "TCM was started again after a failed check")
	dog.assertGone()
}
