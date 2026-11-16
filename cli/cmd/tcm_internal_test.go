package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	tcmCmd "github.com/tarantool/tt/v3/cli/tcm"
)

var errTampered = errors.New("a checked file changed")

// tamperedRepository is an integrity repository whose files have changed.
type tamperedRepository struct{}

func (tamperedRepository) Read(string) (io.ReadCloser, error) { return nil, errTampered }
func (tamperedRepository) ReadFile(string) ([]byte, error)    { return nil, errTampered }
func (tamperedRepository) ValidateAll() error                 { return errTampered }

// tcmShim stands in for TCM: it writes its pid into the file tcm-shim.pid
// and sleeps.
const tcmShim = "#!/bin/sh\necho $$ > tcm-shim.pid\nexec sleep 60\n"

// TestTcmStartRefusedLeavesNoTCM pins that tt tcm start without a watchdog,
// refused because the pid file names a running TCM, leaves no TCM of its own
// running.
func TestTcmStartRefusedLeavesNoTCM(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	running := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, running.Start())
	t.Cleanup(func() {
		_ = running.Process.Kill()
		_ = running.Wait()
	})

	owned, err := process_utils.CreatePIDFile(tcmPidFile, running.Process.Pid)
	require.NoError(t, err)
	require.NoError(t, owned.Keep())

	shim := filepath.Join(dir, "tcm")
	require.NoError(t, os.WriteFile(shim, []byte(tcmShim), 0o700))

	saved := tcmCtx.Executable

	tcmCtx.Executable = shim

	t.Cleanup(func() { tcmCtx.Executable = saved })

	err = startTcmInteractive("INFO")
	require.ErrorContains(t, err, "the pid file belongs to a running process")

	data, err := os.ReadFile(tcmPidFile)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(running.Process.Pid), string(data))

	// A shim left running writes its pid within the wait; one killed at once
	// may never write it.
	deadline := time.Now().Add(2 * time.Second)

	for time.Now().Before(deadline) {
		data, err = os.ReadFile("tcm-shim.pid")
		if err == nil && strings.HasSuffix(string(data), "\n") {
			shimPid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			require.NoError(t, err)
			t.Cleanup(func() { _ = syscall.Kill(shimPid, syscall.SIGKILL) })
			assert.ErrorIs(t, syscall.Kill(shimPid, 0), syscall.ESRCH, "the refused TCM runs")

			return
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// TestTcmWatchdogOpts pins what the watchdog of TCM runs with: the pid files
// the tcm commands read, a 30 second stop timeout, and the integrity check of
// the environment every --integrity-check-period seconds.
func TestTcmWatchdogOpts(t *testing.T) {
	cmdCtx := &cmdcontext.CmdCtx{}

	cmdCtx.Cli.IntegrityCheckPeriod = 42
	cmdCtx.Integrity = integrity.IntegrityCtx{Repository: tamperedRepository{}}

	opts := tcmWatchdogOpts(cmdCtx, "/opt/tcm")

	assert.Equal(t, "/opt/tcm", opts.Executable)
	assert.Equal(t, "watchdog.pid", opts.PidFile)
	assert.Equal(t, "tcm.pid", opts.ChildPidFile)
	assert.Equal(t, 30*time.Second, opts.StopTimeout)
	assert.Equal(t, 42*time.Second, opts.CheckPeriod)
	require.NotNil(t, opts.Check)
	require.ErrorIs(t, opts.Check(t.Context()), errTampered)
}

// TestTcmWatchdogCheckPeriodDefault pins that with integrity checking on and
// no --integrity-check-period, TCM is checked as often as tt instance start
// checks an instance, and not at all with integrity checking off.
func TestTcmWatchdogCheckPeriodDefault(t *testing.T) {
	cmdCtx := &cmdcontext.CmdCtx{}

	assert.Zero(t, tcmWatchdogOpts(cmdCtx, "/opt/tcm").CheckPeriod)

	cmdCtx.Cli.IntegrityCheck = "public.pem"

	assert.Equal(t, time.Duration(integrityCheckPeriod)*time.Second,
		tcmWatchdogOpts(cmdCtx, "/opt/tcm").CheckPeriod)
	assert.Equal(t, 24*time.Hour, time.Duration(integrityCheckPeriod)*time.Second)
}

// pidFileState is how TestTcmStatus leaves a pid file.
type pidFileState int

const (
	// noPidFile leaves none.
	noPidFile pidFileState = iota
	// heldPidFile writes the pid and holds the lock, as its owner does.
	heldPidFile
	// stalePidFile writes the pid and holds no lock, as an owner that died
	// leaves the file.
	stalePidFile
)

// TestTcmStatus pins where tt tcm status finds TCM: through a running
// watchdog first, also while it has no TCM running, then through tcm.pid.
// Whether a process runs is told by the lock of its pid file: a file nobody
// holds names a process that is gone, even when its pid names a live one.
func TestTcmStatus(t *testing.T) {
	live := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, live.Start())
	t.Cleanup(func() {
		_ = live.Process.Kill()
		_ = live.Wait()
	})

	other := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, other.Start())
	t.Cleanup(func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	})

	gone := exec.CommandContext(t.Context(), "true")
	require.NoError(t, gone.Run())

	type pidFile struct {
		state pidFileState
		pid   int
	}

	cases := []struct {
		name     string
		watchdog pidFile
		tcm      pidFile
		code     int
		pid      int
	}{
		{name: "no pid files", code: process_utils.ProcessStoppedCode},
		{
			name:     "watchdog and TCM",
			watchdog: pidFile{heldPidFile, live.Process.Pid},
			tcm:      pidFile{heldPidFile, other.Process.Pid},
			code:     process_utils.ProcessRunningCode, pid: other.Process.Pid,
		},
		{
			name:     "watchdog waiting to restart",
			watchdog: pidFile{heldPidFile, live.Process.Pid},
			code:     process_utils.ProcessRunningCode, pid: live.Process.Pid,
		},
		{
			name: "interactive TCM",
			tcm:  pidFile{heldPidFile, other.Process.Pid},
			code: process_utils.ProcessRunningCode, pid: other.Process.Pid,
		},
		{
			name:     "dead watchdog, interactive TCM",
			watchdog: pidFile{stalePidFile, gone.Process.Pid},
			tcm:      pidFile{heldPidFile, other.Process.Pid},
			code:     process_utils.ProcessRunningCode, pid: other.Process.Pid,
		},
		{
			name: "dead TCM",
			tcm:  pidFile{stalePidFile, gone.Process.Pid},
			code: process_utils.ProcessDeadCode, pid: gone.Process.Pid,
		},
		{
			name: "dead TCM, its pid reused",
			tcm:  pidFile{stalePidFile, other.Process.Pid},
			code: process_utils.ProcessDeadCode, pid: other.Process.Pid,
		},
		{
			name:     "dead watchdog, its pid reused",
			watchdog: pidFile{stalePidFile, live.Process.Pid},
			code:     process_utils.ProcessStoppedCode,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			files := map[string]pidFile{watchdogPidFile: test.watchdog, tcmPidFile: test.tcm}

			for path, file := range files {
				if file.state == noPidFile {
					continue
				}

				require.NoError(t, os.WriteFile(path, []byte(strconv.Itoa(file.pid)), 0o600))

				if file.state == heldPidFile {
					holdLock(t, path)
				}
			}

			status := tcmStatus()
			assert.Equal(t, test.code, status.Code, status.Status)
			assert.Equal(t, test.pid, status.PID)
			require.NoError(t, internalTcmStatus(&cmdcontext.CmdCtx{}, nil))
		})
	}
}

// holdLock holds the lock of the file at path, as its owner does, until the
// test ends.
func holdLock(t *testing.T, path string) {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	require.NoError(t, syscall.Flock(int(file.Fd()), syscall.LOCK_EX))
}

// TestTcmStartInteractiveHoldsLock pins that TCM started without a watchdog
// owns tcm.pid for as long as it runs: it inherits the lock of the file from
// tt tcm start, which exits, so tt tcm status tells it running by the lock,
// and dead once it is gone.
func TestTcmStartInteractiveHoldsLock(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	shim := filepath.Join(dir, "tcm")
	require.NoError(t, os.WriteFile(shim, []byte(tcmShim), 0o700))

	saved := tcmCtx.Executable

	tcmCtx.Executable = shim

	t.Cleanup(func() { tcmCtx.Executable = saved })

	require.NoError(t, startTcmInteractive("INFO"))

	data, err := os.ReadFile(tcmPidFile)
	require.NoError(t, err)

	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	status := tcmStatus()
	assert.Equal(t, process_utils.ProcessRunningCode, status.Code, status.Status)
	assert.Equal(t, pid, status.PID)

	err = startTcmInteractive("INFO")
	require.ErrorContains(t, err, "the pid file belongs to a running process",
		"a second TCM starts next to the first one")

	require.NoError(t, syscall.Kill(pid, syscall.SIGKILL))

	// The killed TCM is a child of this process, which does not reap it: the
	// lock goes as it exits all the same.
	require.Eventually(t, func() bool {
		return tcmStatus().Code == process_utils.ProcessDeadCode
	}, 5*time.Second, 10*time.Millisecond, "the lock outlived TCM")
}

// Environment of TestHelperTcmWatchdog.
const (
	tcmWatchdogHelperEnv = "TT_TCM_STOP_TEST_WATCHDOG"
	tcmStopTimeoutEnv    = "TT_TCM_STOP_TEST_STOP_TIMEOUT"
)

// stubbornTcm stands in for TCM that does not stop on SIGTERM. It writes its
// pid aside and renames the file into place: the test reads the file as soon
// as it is there, and the redirection makes it before echo fills it.
const stubbornTcm = "#!/bin/sh\ntrap '' TERM\n" +
	"echo $$ > tcm-shim.pid.tmp\nmv tcm-shim.pid.tmp tcm-shim.pid\n" +
	"while :; do sleep 0.05; done\n"

// TestHelperTcmWatchdog runs the watchdog of tt tcm start --watchdog for
// TestTcmStopOutwaitsEscalation, in a process of its own.
func TestHelperTcmWatchdog(t *testing.T) {
	if os.Getenv(tcmWatchdogHelperEnv) == "" {
		t.Skip("runs only as the watchdog of TestTcmStopOutwaitsEscalation")
	}

	stopTimeout, err := time.ParseDuration(os.Getenv(tcmStopTimeoutEnv))
	if err != nil {
		panic(err)
	}

	opts := tcmWatchdogOpts(&cmdcontext.CmdCtx{}, "./tcm")

	opts.StopTimeout = stopTimeout

	err = tcmCmd.RunWatchdog(opts)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}

	os.Exit(0)
}

// TestTcmStopOutwaitsEscalation pins that tt tcm stop waits for the watchdog
// to kill TCM that ignores SIGTERM, reap it and remove the pid files, rather
// than giving up first. The escalation is the real one scaled down; the stop
// waits as much longer than it as the real stop does. That margin is not
// scaled: the kill, the reaping and the exit of the watchdog it covers take
// as long on a busy runner whatever the timeouts are, and a tenth of it was
// too little there.
func TestTcmStopOutwaitsEscalation(t *testing.T) {
	const scale = 10

	margin := process_utils.TerminationTimeout - watchdogStopTimeout
	require.Positive(t, margin, "tt tcm stop gives up before the watchdog escalates")

	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile("tcm", []byte(stubbornTcm), 0o700))

	exe, err := os.Executable()
	require.NoError(t, err)

	watchdog := exec.CommandContext(t.Context(), exe, "-test.run=^TestHelperTcmWatchdog$")

	watchdog.Dir = dir

	watchdog.Env = append(os.Environ(), tcmWatchdogHelperEnv+"=1",
		tcmStopTimeoutEnv+"="+(watchdogStopTimeout/scale).String())

	output := &strings.Builder{}

	watchdog.Stdout = output
	watchdog.Stderr = output

	require.NoError(t, watchdog.Start())

	// The watchdog is reaped as soon as it exits, so that the stop sees it
	// gone.
	exited := make(chan struct{})

	go func() {
		_ = watchdog.Wait()

		close(exited)
	}()

	t.Cleanup(func() {
		_ = watchdog.Process.Kill()

		<-exited
	})

	require.Eventually(t, func() bool {
		_, wdErr := os.Stat(watchdogPidFile)
		_, tcmErr := os.Stat(tcmPidFile)
		_, shimErr := os.Stat("tcm-shim.pid")

		return wdErr == nil && tcmErr == nil && shimErr == nil
	}, 10*time.Second, 10*time.Millisecond, "the watchdog did not start TCM")

	data, err := os.ReadFile("tcm-shim.pid")
	require.NoError(t, err)

	shim, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(-shim, syscall.SIGKILL) })

	started := time.Now()

	require.NoError(t, stopTcm(watchdogStopTimeout/scale+margin))
	assert.GreaterOrEqual(t, time.Since(started), watchdogStopTimeout/scale,
		"TCM stopped before the escalation")

	<-exited
	assert.Zero(t, watchdog.ProcessState.ExitCode())
	assert.NoFileExists(t, watchdogPidFile)
	assert.NoFileExists(t, tcmPidFile)
	assert.ErrorIs(t, syscall.Kill(shim, 0), syscall.ESRCH, "TCM outlived the stop")
}
