package supervisor

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartFunc pins that every child is started through Options.Start, with
// the command prepared from the Spec, and that the StartFunc may change what
// runs.
func TestStartFunc(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	require.NoError(t, err)

	spec := helperSpec(t, dir, modeExit, codeEnv+"=0")

	spec.Path = filepath.Join(dir, "not-there")
	spec.Args = []string{"one", "two"}
	spec.Dir = dir
	spec.ProcessGroup = true

	var cmds []*exec.Cmd

	src := fixedSource(spec, true)

	src.restart = func(n int, _ Exit) (bool, error) { return n < 2, nil }

	sup := newHarness(t, src, Options{
		StopSignals: stopSignals,
		Start: func(cmd *exec.Cmd) error {
			cmds = append(cmds, cmd)
			cmd.Path = exe

			return StartCmd(cmd)
		},
	})
	sup.start()
	require.NoError(t, sup.wait())

	require.Len(t, cmds, 2)

	for _, cmd := range cmds {
		assert.Equal(t, []string{spec.Path, "one", "two"}, cmd.Args)
		assert.Equal(t, dir, cmd.Dir)
		assert.Equal(t, spec.Env, cmd.Env)
		assert.True(t, cmd.SysProcAttr.Setpgid)
	}

	exits, _ := eventsOf[Exit](sup.rec)
	require.Len(t, exits, 2)

	for _, exit := range exits {
		assert.True(t, exit.State.Success())
	}
}

// TestStartFuncWithoutProcess pins that a StartFunc reporting success
// without starting anything is an error, not a crash.
func TestStartFuncWithoutProcess(t *testing.T) {
	dir := t.TempDir()
	sup := newHarness(t, fixedSource(helperSpec(t, dir, modeServe), false), Options{
		Start: func(*exec.Cmd) error { return nil },
	})
	sup.start()

	err := sup.wait()
	require.ErrorIs(t, err, ErrNotStarted)
	assert.Equal(t, OpStart, errorOp(t, err))
}

// TestStartFuncStartsAndFails pins that a process the StartFunc started but
// did not hand over, because the StartFunc panicked or failed after starting
// it, is killed and waited for, with its group, and does not outlive Run.
func TestStartFuncStartsAndFails(t *testing.T) {
	cases := []struct {
		name   string
		panics bool
		group  bool
	}{
		{name: "panic", panics: true},
		{name: "panic in a group", panics: true, group: true},
		{name: "error", panics: false},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "supervisor.pid")
			spec := helperSpec(t, dir, modeStubborn)

			if test.group {
				spec = helperSpec(t, dir, modeFamily)
				spec.ProcessGroup = true
			}

			var (
				started  atomic.Int64
				cleanups atomic.Int32
			)

			engine, err := New(fixedSource(spec, true), Options{
				PidFile:     pidFile,
				StopSignals: stopSignals,
				Cleanup:     func() { cleanups.Add(1) },
				Start: func(cmd *exec.Cmd) error {
					err := StartCmd(cmd)
					if err != nil {
						return err
					}

					started.Store(int64(cmd.Process.Pid))
					// Only a child that is well under way proves anything.
					waitReady(t, dir, cmd.Process.Pid)

					if test.panics {
						panic(errPanic)
					}

					return errTest
				},
			})
			require.NoError(t, err)

			engine.subscribe = func() (<-chan os.Signal, func()) {
				return make(chan os.Signal), func() {}
			}

			var (
				runErr    error
				recovered any
			)

			func() {
				defer func() { recovered = recover() }()

				runErr = engine.Run(t.Context())
			}()

			pid := int(started.Load())
			require.NotZero(t, pid)
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

			if test.panics {
				assert.Equal(t, errPanic, recovered)
			} else {
				require.ErrorIs(t, runErr, errTest)
				assert.Equal(t, OpStart, errorOp(t, runErr))
			}

			require.ErrorIs(t, syscall.Kill(pid, 0), syscall.ESRCH,
				"the child outlived Run or was not waited for")

			if test.group {
				grandchild, err := strconv.Atoi(waitFile(t, filepath.Join(dir, "grandchild")))
				require.NoError(t, err)
				t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })
				assert.Eventually(t, func() bool {
					return errors.Is(syscall.Kill(grandchild, 0), syscall.ESRCH)
				}, waitTimeout, pollInterval, "the group outlived Run")
			}

			assert.Equal(t, int32(1), cleanups.Load())
			assert.NoFileExists(t, pidFile)
		})
	}
}

// TestStdin pins that Spec.Stdin reaches the child's standard input and
// that without it the child reads an empty one.
func TestStdin(t *testing.T) {
	for _, input := range [][]byte{[]byte("local launcher = true\n"), nil} {
		dir := t.TempDir()
		spec := helperSpec(t, dir, modeServe, stdinEnv+"=1")

		spec.Stdin = input

		sup := newHarness(t, fixedSource(spec, false), Options{StopSignals: stopSignals})
		sup.start()

		started := waitEvent[Started](t, sup.rec, 1)
		waitReady(t, dir, started.Pid)

		data, err := os.ReadFile(helperFile(dir, "stdin", started.Pid))
		require.NoError(t, err)
		assert.Equal(t, string(input), string(data))

		sup.send(syscall.SIGTERM)
		require.NoError(t, sup.wait())
	}
}

// TestProcessGroup pins that with ProcessGroup the child leads its own
// group, signals reach the whole group, and the stop timeout kills the whole
// group; without it the child stays in the supervisor's group.
func TestProcessGroup(t *testing.T) {
	t.Run("own group", func(t *testing.T) {
		dir := t.TempDir()
		spec := helperSpec(t, dir, modeFamily)

		spec.ProcessGroup = true
		spec.StopTimeout = 300 * time.Millisecond

		sup := newHarness(t, fixedSource(spec, true), Options{StopSignals: stopSignals})
		sup.start()

		started := waitEvent[Started](t, sup.rec, 1)
		grandchild, err := strconv.Atoi(waitFile(t, filepath.Join(dir, "grandchild")))
		require.NoError(t, err)
		t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

		waitReady(t, dir, started.Pid)
		waitReady(t, dir, grandchild)

		pgid, err := syscall.Getpgid(started.Pid)
		require.NoError(t, err)
		assert.Equal(t, started.Pid, pgid)

		sup.send(syscall.SIGUSR1)
		waitSignals(t, dir, started.Pid, syscall.SIGUSR1)
		waitSignals(t, dir, grandchild, syscall.SIGUSR1)

		sup.send(syscall.SIGTERM)
		require.NoError(t, sup.wait())

		kills, _ := eventsOf[Killed](sup.rec)
		require.Len(t, kills, 1)
		assert.Equal(t, KillStopTimeout, kills[0].Reason)
		// The grandchild is not ours to wait for; its new parent reaps it.
		assert.Eventually(t, func() bool {
			return syscall.Kill(grandchild, 0) == syscall.ESRCH
		}, waitTimeout, pollInterval, "the grandchild survived the group kill")
	})

	t.Run("shared group", func(t *testing.T) {
		dir := t.TempDir()
		sup := newHarness(t, fixedSource(helperSpec(t, dir, modeServe), true),
			Options{StopSignals: stopSignals})
		sup.start()

		started := waitEvent[Started](t, sup.rec, 1)
		pgid, err := syscall.Getpgid(started.Pid)
		require.NoError(t, err)
		assert.Equal(t, syscall.Getpgrp(), pgid)

		waitReady(t, dir, started.Pid)
		sup.send(syscall.SIGTERM)
		require.NoError(t, sup.wait())
	})
}

// TestGrandchildHoldsOutput pins that an exited child is reported once its
// output has been drained for at most the stop timeout, even while a
// grandchild keeps the output pipe open.
func TestGrandchildHoldsOutput(t *testing.T) {
	const stopTimeout = 300 * time.Millisecond

	dir := t.TempDir()
	spec := helperSpec(t, dir, modeOrphan)

	spec.Stdout = io.Discard
	spec.StopTimeout = stopTimeout

	sup := newHarness(t, fixedSource(spec, false), Options{StopSignals: stopSignals})
	sup.start()

	grandchild, err := strconv.Atoi(waitFile(t, filepath.Join(dir, "grandchild")))
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	started := waitEvent[Started](t, sup.rec, 1)

	select {
	case err := <-sup.done:
		sup.done <- err
	case <-time.After(10 * stopTimeout):
		require.FailNow(t, "Run hangs on the output a grandchild holds")
	}

	require.NoError(t, sup.wait())

	exit := waitEvent[Exit](t, sup.rec, 1)
	assert.Equal(t, started.Pid, exit.Pid)
	assert.True(t, exit.State.Success())
	require.ErrorIs(t, exit.Err, exec.ErrWaitDelay)
	assert.NoError(t, syscall.Kill(grandchild, 0), "the grandchild is not ours to kill")
}

// TestSpecCommand pins how a Spec runs without a supervisor: cancelling the
// context sends the stop signal, and a process that ignores it is killed a
// stop timeout later.
func TestSpecCommand(t *testing.T) {
	cases := []struct {
		name   string
		mode   string
		code   int
		signal syscall.Signal
	}{
		{name: "stops", mode: modeServe, code: exitOnInt},
		{name: "killed", mode: modeStubborn, code: -1, signal: syscall.SIGKILL},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			spec := helperSpec(t, dir, test.mode)

			spec.StopSignal = syscall.SIGINT
			spec.StopTimeout = 300 * time.Millisecond

			ctx, cancel := context.WithCancel(t.Context())
			cmd := spec.Command(ctx)

			require.NoError(t, cmd.Start())
			waitReady(t, dir, cmd.Process.Pid)

			cancelled := time.Now()

			cancel()

			err := cmd.Wait()
			require.Error(t, err)
			assert.Equal(t, test.code, cmd.ProcessState.ExitCode())
			assert.Equal(t, test.signal, termSignal(cmd.ProcessState))

			if test.signal == syscall.SIGKILL {
				assert.GreaterOrEqual(t, time.Since(cancelled), spec.StopTimeout)
			}
		})
	}
}

// TestDetach pins that a detached process leads its own process group and is
// waited for once it dies, and that Detach fails when the StartFunc does.
func TestDetach(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(helperEnv, modeStubborn)
	t.Setenv(dirEnv, dir)
	t.Setenv("GORACE", "atexit_sleep_ms=0")

	exe, err := os.Executable()
	require.NoError(t, err)

	pid, err := Detach(exe, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	waitReady(t, dir, pid)

	pgid, err := syscall.Getpgid(pid)
	require.NoError(t, err)
	assert.Equal(t, pid, pgid)

	// Once it exits, Detach has waited for it: no zombie is left behind in a
	// caller that lives on.
	require.NoError(t, syscall.Kill(pid, syscall.SIGKILL))
	waitReaped(t, pid)

	_, err = Detach(exe, nil, func(*exec.Cmd) error { return errTest })
	require.ErrorIs(t, err, errTest)
}

// TestDetachReaps pins that a detached process that exits on its own is
// waited for.
func TestDetachReaps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(helperEnv, modeExit)
	t.Setenv(dirEnv, dir)
	t.Setenv(codeEnv, "0")
	t.Setenv("GORACE", "atexit_sleep_ms=0")

	exe, err := os.Executable()
	require.NoError(t, err)

	pid, err := Detach(exe, nil, nil)
	require.NoError(t, err)
	waitReaped(t, pid)
}
