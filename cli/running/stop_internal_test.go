package running

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/internal/pidfile"
)

// Each helper keeps running until every helper has received SIGINT. Waiting
// for even one process before interrupting the others therefore cannot finish.
func TestStopAllInterruptsBeforeWaiting(t *testing.T) {
	for _, withErrors := range []bool{false, true} {
		t.Run(strconv.FormatBool(withErrors), func(t *testing.T) {
			dir := t.TempDir()
			first := startStopTestProcess(t, dir, 0, 2)
			second := startStopTestProcess(t, dir, 1, 2)
			runs := []InstanceCtx{first.inst, second.inst}

			if withErrors {
				// A pid file that cannot be read.
				invalid := InstanceCtx{
					AppName: "invalid", PIDFile: filepath.Join(dir, "invalid.pid"),
				}
				require.NoError(t, os.Mkdir(invalid.PIDFile, 0o700))

				other := invalid

				other.AppName = "other-invalid"

				missing := InstanceCtx{
					AppName: "stopped", PIDFile: filepath.Join(dir, "missing.pid"),
				}

				runs = []InstanceCtx{invalid, first.inst, missing, other, second.inst}
			}

			done := make(chan struct{})

			var stopErr error

			go func() {
				stopErr = StopAll(runs)

				close(done)
			}()

			// Ensure a failing assertion also releases an implementation that
			// is waiting for a helper before it signals the next one.
			t.Cleanup(func() {
				_ = first.cmd.Process.Kill()
				_ = second.cmd.Process.Kill()

				select {
				case <-done:
				case <-time.After(stopTimeout):
					t.Error("batch stop did not finish after helper cleanup")
				}
			})

			require.Eventually(t, func() bool {
				return stopTestFileExists(filepath.Join(dir, "0.interrupted")) &&
					stopTestFileExists(filepath.Join(dir, "1.interrupted"))
			}, stopTimeout, 10*time.Millisecond, "all processes must be interrupted before waiting")

			select {
			case <-done:
				if withErrors {
					require.ErrorContains(t, stopErr, "failed to stop instance invalid:")
					require.ErrorContains(t, stopErr, "failed to stop instance other-invalid:")
					assert.NotContains(t, stopErr.Error(), "stopped:")
				} else {
					require.NoError(t, stopErr)
				}
			case <-time.After(stopTimeout):
				t.Fatal("batch stop did not wait for the interrupted processes to exit")
			}

			require.NoError(t, first.wait())
			require.NoError(t, second.wait())
		})
	}
}

func TestStopSingle(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		proc := startStopTestProcess(t, t.TempDir(), 0, 1)
		require.NoError(t, Stop(&proc.inst))
		require.NoError(t, proc.wait())
	})

	t.Run("missing pid", func(t *testing.T) {
		inst := InstanceCtx{AppName: "stopped", PIDFile: filepath.Join(t.TempDir(), "missing.pid")}
		require.NoError(t, Stop(&inst))
	})

	t.Run("unreadable pid file", func(t *testing.T) {
		inst := InstanceCtx{AppName: "invalid", PIDFile: filepath.Join(t.TempDir(), "invalid.pid")}
		require.NoError(t, os.Mkdir(inst.PIDFile, 0o700))
		require.ErrorContains(t, Stop(&inst), "failed to stop instance invalid:")
	})

	t.Run("empty batch", func(t *testing.T) {
		require.NoError(t, StopAll(nil))
	})
}

// TestStopStale pins that tt instance stop takes an instance whose pid file
// nobody holds for stopped, whatever the pid in it names, signals nothing,
// and removes the file and the sockets the dead watchdog left.
func TestStopStale(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"no pid":         func(*testing.T) string { return "not a pid" },
		"a process gone": func(*testing.T) string { return strconv.Itoa(1<<31 - 2) },
		"a live process": func(t *testing.T) string {
			t.Helper()

			return strconv.Itoa(stopVictim(t))
		},
		"this test itself": func(*testing.T) string { return strconv.Itoa(os.Getpid()) },
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			run := killRun(t)

			run.AppName, run.InstName = "app", "inst"
			require.NoError(t, os.WriteFile(run.PIDFile, []byte(content(t)), 0o600))

			// SIGINT would end the test process.
			interrupt := make(chan os.Signal, 1)
			signal.Notify(interrupt, syscall.SIGINT)

			defer signal.Stop(interrupt)

			require.NoError(t, Stop(run))
			assert.NoFileExists(t, run.PIDFile)
			assert.NoFileExists(t, run.ConsoleSocket)
			assert.NoFileExists(t, run.BinaryPort)
			assert.Equal(t, process_utils.ProcessStoppedCode, Status(run).Code)

			select {
			case <-interrupt:
				t.Error("the test process got SIGINT")
			default:
			}
		})
	}
}

// stopVictim starts a process that stands for an unrelated process that has
// taken the pid of a dead watchdog, and returns its pid. The test fails if
// the process exits before the test ends: it got a signal meant for the
// watchdog.
func stopVictim(t *testing.T) int {
	t.Helper()

	cmd := sleeper(t, false)

	t.Cleanup(func() {
		assert.True(t, stillRuns(cmd.Process.Pid), "the unrelated process got a signal")
	})

	return cmd.Process.Pid
}

// TestStatusByOwner pins how the state of an instance is told from its pid
// file: by whether a watchdog holds it, not by whether the pid in it names a
// live process.
func TestStatusByOwner(t *testing.T) {
	run := killRun(t)

	assert.Equal(t, process_utils.ProcessStoppedCode, Status(run).Code)

	require.NoError(t, os.WriteFile(run.PIDFile, []byte("not a pid"), 0o600))
	assert.Equal(t, process_utils.ProcessStoppedCode, Status(run).Code)

	other := sleeper(t, false).Process.Pid
	require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(other)), 0o600))

	state := Status(run)
	assert.Equal(t, process_utils.ProcessDeadCode, state.Code, "a stale file names a live process")
	assert.Equal(t, other, state.PID)
	assert.False(t, IsInstanceActive(run))
	assert.True(t, IsStopped(run))

	require.NoError(t, os.Remove(run.PIDFile))

	watchdog := fakeWatchdog(t, run)

	state = Status(run)
	assert.Equal(t, process_utils.ProcessRunningCode, state.Code)
	assert.Equal(t, watchdog.Process.Pid, state.PID)
	assert.False(t, IsStopped(run))

	stopped, err := SettleStopped(run)
	require.NoError(t, err)
	assert.False(t, stopped)
	assert.FileExists(t, run.PIDFile)
	assert.FileExists(t, run.ConsoleSocket)

	require.NoError(t, watchdog.Process.Kill())

	_, err = watchdog.Process.Wait()
	require.NoError(t, err)

	state = Status(run)
	assert.Equal(t, process_utils.ProcessDeadCode, state.Code)
	assert.Equal(t, watchdog.Process.Pid, state.PID)

	stopped, err = SettleStopped(run)
	require.NoError(t, err)
	assert.True(t, stopped)
	assert.NoFileExists(t, run.PIDFile)
	assert.NoFileExists(t, run.ConsoleSocket)
	assert.NoFileExists(t, run.BinaryPort)
	assert.Equal(t, process_utils.ProcessStoppedCode, Status(run).Code)
}

// TestClearForWatchdog pins what tt instance start makes of the pid file of
// an instance before it starts a watchdog: one that a watchdog holds means
// the instance runs already, while one nobody holds goes, also when the pid
// in it names a live process, which the new watchdog would refuse.
func TestClearForWatchdog(t *testing.T) {
	run := killRun(t)

	pid, err := clearForWatchdog(run)
	require.NoError(t, err)
	assert.Zero(t, pid)

	other := sleeper(t, false).Process.Pid
	require.NoError(t, os.WriteFile(run.PIDFile, []byte(strconv.Itoa(other)), 0o600))

	pid, err = clearForWatchdog(run)
	require.NoError(t, err)
	assert.Zero(t, pid)
	assert.NoFileExists(t, run.PIDFile)
	assert.NoFileExists(t, run.ConsoleSocket)
	assert.True(t, stillRuns(other))

	owned, err := pidfile.Acquire(run.PIDFile, os.Getpid())
	require.NoError(t, err, "the watchdog cannot take the pid file")

	pid, err = clearForWatchdog(run)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), pid, "a watchdog holds the pid file")
	require.NoError(t, owned.Release())
}

type stopTestProcess struct {
	inst    InstanceCtx
	cmd     *exec.Cmd
	done    chan struct{}
	waitErr error
}

func (proc *stopTestProcess) wait() error {
	<-proc.done

	return proc.waitErr
}

func startStopTestProcess(t *testing.T, dir string, index, count int) *stopTestProcess {
	t.Helper()

	name := strconv.Itoa(index)
	exe, err := os.Executable()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestStopProcessHelper$")

	cmd.Env = append(os.Environ(), "TT_STOP_HELPER_DIR="+dir,
		"TT_STOP_HELPER_INDEX="+name, "TT_STOP_HELPER_COUNT="+strconv.Itoa(count))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	proc := &stopTestProcess{
		inst: InstanceCtx{AppName: "app", InstName: name, PIDFile: filepath.Join(dir, name+".pid")},
		cmd:  cmd,
		done: make(chan struct{}),
	}
	require.NoError(t, cmd.Start())

	go func() {
		proc.waitErr = cmd.Wait()
		close(proc.done)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()

		<-proc.done
	})

	// The helper holds its pid file, as a watchdog does, before it is ready.
	require.Eventually(t, func() bool {
		return stopTestFileExists(filepath.Join(dir, name+".ready"))
	}, stopTimeout, 10*time.Millisecond)

	return proc
}

func stopTestFileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

func TestStopProcessHelper(t *testing.T) {
	dir := os.Getenv("TT_STOP_HELPER_DIR")
	if dir == "" {
		return
	}

	name := os.Getenv("TT_STOP_HELPER_INDEX")
	count, err := strconv.Atoi(os.Getenv("TT_STOP_HELPER_COUNT"))
	require.NoError(t, err)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT)

	defer signal.Stop(interrupt)

	owned, err := pidfile.Acquire(filepath.Join(dir, name+".pid"), os.Getpid())
	require.NoError(t, err)

	defer func() { require.NoError(t, owned.Release()) }()

	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".ready"), nil, 0o600))
	<-interrupt
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".interrupted"), nil, 0o600))
	require.Eventually(t, func() bool {
		for index := range count {
			if !stopTestFileExists(filepath.Join(dir, strconv.Itoa(index)+".interrupted")) {
				return false
			}
		}

		return true
	}, 3*stopTimeout, 10*time.Millisecond)
}
