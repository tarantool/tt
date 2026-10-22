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
				invalid := InstanceCtx{
					AppName: "invalid", PIDFile: filepath.Join(dir, "invalid.pid"),
				}
				require.NoError(t, os.WriteFile(invalid.PIDFile, []byte("not a pid"), 0o600))

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

	t.Run("invalid pid", func(t *testing.T) {
		inst := InstanceCtx{AppName: "invalid", PIDFile: filepath.Join(t.TempDir(), "invalid.pid")}
		require.NoError(t, os.WriteFile(inst.PIDFile, []byte("not a pid"), 0o600))
		require.ErrorContains(t, Stop(&inst), "failed to stop instance invalid:")
	})

	t.Run("empty batch", func(t *testing.T) {
		require.NoError(t, StopAll(nil))
	})
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

	require.Eventually(t, func() bool {
		return stopTestFileExists(filepath.Join(dir, name+".ready"))
	}, stopTimeout, 10*time.Millisecond)

	pid := []byte(strconv.Itoa(cmd.Process.Pid))
	require.NoError(t, os.WriteFile(proc.inst.PIDFile, pid, 0o600))

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
