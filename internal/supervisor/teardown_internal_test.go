package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCleanupOnEveryExit pins, for every way Run can end after it created
// its pid file, that the child is gone before Cleanup runs, Cleanup runs
// once and before the supervisor pid file goes, and no pid file of ours is
// left.
func TestCleanupOnEveryExit(t *testing.T) {
	type setup struct {
		dir      string
		spec     Spec
		src      *testSource
		opts     Options
		childPid string
	}

	cases := []struct {
		name    string
		prepare func(t *testing.T, state *setup)
		act     func(t *testing.T, sup *harness, state *setup)
		op      Op
	}{
		{
			name: "stop signal",
			act: func(t *testing.T, sup *harness, state *setup) {
				t.Helper()

				started := waitEvent[Started](t, sup.rec, 1)
				waitReady(t, state.dir, started.Pid)
				sup.send(syscall.SIGTERM)
			},
		},
		{
			name: "cancelled context",
			act: func(t *testing.T, sup *harness, state *setup) {
				t.Helper()

				started := waitEvent[Started](t, sup.rec, 1)
				waitReady(t, state.dir, started.Pid)
				sup.cancel()
			},
		},
		{
			name: "no restart",
			prepare: func(t *testing.T, state *setup) {
				t.Helper()

				state.spec = helperSpec(t, state.dir, modeExit, codeEnv+"=0")
			},
		},
		{
			name: "stop during restart delay",
			prepare: func(t *testing.T, state *setup) {
				t.Helper()

				state.spec = helperSpec(t, state.dir, modeExit, codeEnv+"=1")
				state.src.restart = func(int, Exit) (bool, error) { return true, nil }
				state.opts.RestartDelay = time.Hour
			},
			act: func(t *testing.T, sup *harness, _ *setup) {
				t.Helper()

				waitEvent[Restarting](t, sup.rec, 1)
				sup.send(syscall.SIGINT)
			},
		},
		{
			name: "source fails",
			prepare: func(_ *testing.T, state *setup) {
				state.src.next = func(context.Context, int) (Spec, error) { return Spec{}, errTest }
			},
			op: OpNext,
		},
		{
			name: "invalid spec",
			prepare: func(_ *testing.T, state *setup) {
				state.spec = Spec{}
			},
			op: OpNext,
		},
		{
			name: "start fails",
			prepare: func(_ *testing.T, state *setup) {
				state.spec.Path = filepath.Join(state.dir, "missing")
			},
			op: OpStart,
		},
		{
			name: "restart decision fails",
			prepare: func(t *testing.T, state *setup) {
				t.Helper()

				state.spec = helperSpec(t, state.dir, modeExit, codeEnv+"=1")
				state.src.restart = func(int, Exit) (bool, error) { return false, errTest }
			},
			op: OpRestart,
		},
		{
			name: "check fails",
			prepare: func(t *testing.T, state *setup) {
				t.Helper()

				state.spec = helperSpec(t, state.dir, modeStubborn)
				state.opts.CheckPeriod = 20 * time.Millisecond
				state.opts.Check = func(context.Context) error { return errTest }
			},
			op: OpCheck,
		},
		{
			name: "child pid file refused",
			prepare: func(t *testing.T, state *setup) {
				t.Helper()

				state.childPid = strconv.Itoa(os.Getppid())
				require.NoError(t,
					os.WriteFile(state.opts.ChildPidFile, []byte(state.childPid), 0o600))
			},
			op: OpChildPidFile,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			state := &setup{
				dir:  dir,
				spec: helperSpec(t, dir, modeServe),
				opts: Options{
					PidFile:      filepath.Join(dir, "supervisor.pid"),
					ChildPidFile: filepath.Join(dir, "child.pid"),
					StopSignals:  stopSignals,
				},
			}

			state.src = &testSource{restart: func(int, Exit) (bool, error) { return false, nil }}
			state.src.next = func(context.Context, int) (Spec, error) { return state.spec, nil }

			if test.prepare != nil {
				test.prepare(t, state)
			}

			var (
				cleanups         atomic.Int32
				pidFileAtCleanup bool
				sup              *harness
			)

			state.opts.Cleanup = func() {
				cleanups.Add(1)

				_, err := os.Stat(state.opts.PidFile)

				pidFileAtCleanup = err == nil

				sup.assertNoChildren()
			}
			sup = newHarness(t, state.src, state.opts)
			sup.start()

			if test.act != nil {
				test.act(t, sup, state)
			}

			err := sup.wait()
			if test.op == 0 {
				require.NoError(t, err)
			} else {
				assert.Equal(t, test.op, errorOp(t, err))
			}

			assert.Equal(t, int32(1), cleanups.Load())
			assert.True(t, pidFileAtCleanup, "the supervisor pid file went before Cleanup")
			assert.NoFileExists(t, state.opts.PidFile)
			sup.assertNoChildren()

			if state.childPid == "" {
				assert.NoFileExists(t, state.opts.ChildPidFile)
			} else {
				data, err := os.ReadFile(state.opts.ChildPidFile)
				require.NoError(t, err)
				assert.Equal(t, state.childPid, string(data), "a pid file of another process")

				kills, _ := eventsOf[Killed](sup.rec)
				require.Len(t, kills, 1)
				assert.Equal(t, KillChildPidFile, kills[0].Reason)
			}
		})
	}
}

// TestPanicTearsDown pins that a callback panicking out of Run still tears
// the supervision down: the child is killed and waited for, the checks stop,
// Cleanup runs, the pid files go, and the panic reaches the caller.
func TestPanicTearsDown(t *testing.T) {
	cases := []struct {
		name string
		// panics tells whether the callback panics on this event.
		panics func(event Event) bool
		// send is a signal sent once the child runs, 0 for none.
		send syscall.Signal
		// cleanupPanics makes Cleanup itself panic.
		cleanupPanics bool
	}{
		{
			name: "on started",
			panics: func(event Event) bool {
				_, ok := event.(Started)

				return ok
			},
		},
		{
			name: "on a signal",
			panics: func(event Event) bool {
				_, ok := event.(SignalReceived)

				return ok
			},
			send: syscall.SIGUSR1,
		},
		{
			name: "in cleanup",
			panics: func(event Event) bool {
				_, ok := event.(Started)

				return ok
			},
			cleanupPanics: true,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "supervisor.pid")
			childPidFile := filepath.Join(dir, "child.pid")
			src := fixedSource(helperSpec(t, dir, modeStubborn), true)

			var (
				child    atomic.Int64
				checks   atomic.Int32
				cleanups atomic.Int32
			)

			signals := make(chan os.Signal, 1)
			engine, err := New(src, Options{
				PidFile:      pidFile,
				ChildPidFile: childPidFile,
				StopSignals:  stopSignals,
				CheckPeriod:  5 * time.Millisecond,
				Check: func(context.Context) error {
					checks.Add(1)

					return nil
				},
				Cleanup: func() {
					cleanups.Add(1)

					if test.cleanupPanics {
						panic(errPanic)
					}
				},
				OnEvent: func(event Event) {
					started, ok := event.(Started)
					if ok {
						child.Store(int64(started.Pid))

						if test.send != 0 {
							waitReady(t, dir, started.Pid)

							signals <- test.send
						}
					}

					if test.panics(event) {
						panic(errPanic)
					}
				},
			})
			require.NoError(t, err)

			engine.subscribe = func() (<-chan os.Signal, func()) { return signals, func() {} }

			recovered := make(chan any, 1)

			go func() {
				defer func() { recovered <- recover() }()

				_ = engine.Run(context.Background())
			}()

			select {
			case value := <-recovered:
				assert.Equal(t, errPanic, value)
			case <-time.After(waitTimeout):
				require.FailNow(t, "Run neither returned nor panicked")
			}

			pid := int(child.Load())
			require.NotZero(t, pid)
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

			require.ErrorIs(t, syscall.Kill(pid, 0), syscall.ESRCH,
				"the child was not killed and waited for")
			assert.Equal(t, int32(1), cleanups.Load())
			assert.NoFileExists(t, pidFile)
			assert.NoFileExists(t, childPidFile)

			stopped := checks.Load()

			time.Sleep(50 * time.Millisecond)
			assert.Equal(t, stopped, checks.Load(), "the checks outlived Run")
		})
	}
}
