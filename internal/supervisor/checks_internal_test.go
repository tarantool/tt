package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckFailureKillsChild pins that a check that fails, with an error or
// by panicking, kills the child, even one that ignores the stop signals, and
// ends Run with the failure instead of the restart the Source would grant.
func TestCheckFailureKillsChild(t *testing.T) {
	cases := []struct {
		name string
		// passes is how many checks pass before the one that fails.
		passes int
		fail   func() error
		// panics tells that fail panics rather than returns.
		panics  bool
		wantErr error
	}{
		{
			name:    "error",
			passes:  2,
			fail:    func() error { return errTest },
			wantErr: errTest,
		},
		{
			name:    "panic",
			passes:  0,
			fail:    func() error { panic(errPanic) },
			panics:  true,
			wantErr: ErrCheckPanicked,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			src := fixedSource(helperSpec(t, dir, modeStubborn), true)

			var checks atomic.Int32

			sup := newHarness(t, src, Options{
				StopSignals: stopSignals,
				CheckPeriod: 20 * time.Millisecond,
				Check: func(context.Context) error {
					if int(checks.Add(1)) <= test.passes {
						return nil
					}

					return test.fail()
				},
			})
			sup.start()

			err := sup.wait()
			require.ErrorIs(t, err, test.wantErr)
			assert.Equal(t, OpCheck, errorOp(t, err))

			results, _ := eventsOf[Checked](sup.rec)
			require.Len(t, results, test.passes+1)

			for _, result := range results[:test.passes] {
				assert.Equal(t, Checked{}, result)
			}

			if test.panics {
				assert.Contains(t, err.Error(), errPanic.Error())
				require.ErrorIs(t, results[test.passes].Err, ErrCheckPanicked)
			} else {
				assert.Equal(t, Checked{Err: test.wantErr}, results[test.passes])
			}

			kills, _ := eventsOf[Killed](sup.rec)
			require.Len(t, kills, 1)
			assert.Equal(t, KillCheckFailed, kills[0].Reason)

			exits, _ := eventsOf[Exit](sup.rec)
			require.Len(t, exits, 1)
			assert.Equal(t, syscall.SIGKILL, termSignal(exits[0].State))

			nexts, restarts := src.calls()
			assert.Equal(t, 1, nexts)
			assert.Zero(t, restarts, "the Source was asked about a restart")
			assert.Equal(t, test.passes+1, int(checks.Load()))
			sup.assertNoChildren()
		})
	}
}

// checkWhileChildExits returns a Check that, on its first run, kills the
// child and then waits until the engine has seen the exit and cancelled it,
// and only then returns what finish makes of its context. Every later run
// passes. The child pid comes from the Started events.
func checkWhileChildExits(child *atomic.Int64, finish func(ctx context.Context) error,
) func(ctx context.Context) error {
	var runs atomic.Int32

	return func(ctx context.Context) error {
		if runs.Add(1) > 1 {
			return nil
		}

		_ = syscall.Kill(int(child.Load()), syscall.SIGKILL)

		<-ctx.Done()

		return finish(ctx)
	}
}

// TestCheckResultAsChildExits pins what a check counts for when the child
// exits under it and it returns only once cancelled. Whatever it found, an
// error or a panic, even reported together with the cancellation, is a hard
// stop: Run ends with it, and the Source, which would restart, is not asked.
// A check that reports nothing but the cancellation has not failed, and the
// Source decides on the restart as after any other exit.
func TestCheckResultAsChildExits(t *testing.T) {
	cases := []struct {
		name   string
		finish func(ctx context.Context) error
		// wantErr is what Run ends with, nil when the check has not failed.
		wantErr error
	}{
		{
			name:    "failure is a hard stop",
			finish:  func(context.Context) error { return errTest },
			wantErr: errTest,
		},
		{
			name: "failure joined with the cancellation is a hard stop",
			finish: func(ctx context.Context) error {
				return errors.Join(errTest, ctx.Err())
			},
			wantErr: errTest,
		},
		{
			name: "failure wrapped with the cancellation is a hard stop",
			finish: func(ctx context.Context) error {
				return fmt.Errorf("%w: %w", errTest, ctx.Err())
			},
			wantErr: errTest,
		},
		{
			name:    "panic is a hard stop",
			finish:  func(context.Context) error { panic(errPanic) },
			wantErr: ErrCheckPanicked,
		},
		{
			name: "cancellation is not a failure",
			finish: func(ctx context.Context) error {
				return fmt.Errorf("check interrupted: %w", ctx.Err())
			},
			wantErr: nil,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			hardStop := test.wantErr != nil
			dir := t.TempDir()
			// A Source that restarts after a hard stop shows that it was not
			// asked; one that declines otherwise ends Run after the restart
			// decision.
			src := fixedSource(helperSpec(t, dir, modeServe), hardStop)

			var (
				child atomic.Int64
				// returned is what the check returned; Run returns after the
				// check has.
				returned error
			)

			rec := &recorder{}
			sup := newHarness(t, src, Options{
				StopSignals: stopSignals,
				CheckPeriod: 20 * time.Millisecond,
				Check: checkWhileChildExits(&child, func(ctx context.Context) error {
					returned = test.finish(ctx)

					return returned
				}),
				OnEvent: func(event Event) {
					started, ok := event.(Started)
					if ok {
						child.Store(int64(started.Pid))
					}

					rec.record(event)
				},
			})

			sup.rec = rec
			sup.start()

			err := sup.wait()
			nexts, restarts := src.calls()
			checked, _ := eventsOf[Checked](rec)

			assert.Equal(t, 1, nexts)
			sup.assertNoChildren()

			switch {
			case !hardStop:
				require.NoError(t, err)
				assert.Equal(t, 1, restarts)
				assert.Empty(t, checked)
			case errors.Is(test.wantErr, ErrCheckPanicked):
				require.ErrorIs(t, err, ErrCheckPanicked)
				assert.Equal(t, OpCheck, errorOp(t, err))
				assert.Contains(t, err.Error(), errPanic.Error())
				assert.Zero(t, restarts, "the Source was asked about a restart")
				require.Len(t, checked, 1)
				require.ErrorIs(t, checked[0].Err, ErrCheckPanicked)
			default:
				require.ErrorIs(t, err, test.wantErr)
				assert.Equal(t, OpCheck, errorOp(t, err))
				assert.Zero(t, restarts, "the Source was asked about a restart")
				assert.Equal(t, []Checked{{Err: returned}}, checked)
			}
		})
	}
}

// TestChecksFollowTheChild pins that checks run while a child runs and never
// while none does, the restart delay included.
func TestChecksFollowTheChild(t *testing.T) {
	dir := t.TempDir()
	src := fixedSource(helperSpec(t, dir, modeServe), true)

	var (
		running         atomic.Bool
		checks, strayed atomic.Int32
	)

	rec := &recorder{}
	sup := newHarness(t, src, Options{
		StopSignals:  stopSignals,
		RestartDelay: 300 * time.Millisecond,
		CheckPeriod:  10 * time.Millisecond,
		Check: func(context.Context) error {
			checks.Add(1)

			if !running.Load() {
				strayed.Add(1)
			}

			return nil
		},
		OnEvent: func(event Event) {
			switch event.(type) {
			case Started:
				running.Store(true)
			case Exit:
				running.Store(false)
			}

			rec.record(event)
		},
	})

	sup.rec = rec
	sup.start()

	first := waitEvent[Started](t, rec, 1)
	waitEvent[Checked](t, rec, 3)
	require.NoError(t, syscall.Kill(first.Pid, syscall.SIGKILL))

	second := waitEvent[Started](t, rec, 2)
	waitReady(t, dir, second.Pid)
	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())

	assert.GreaterOrEqual(t, checks.Load(), int32(3))
	assert.Zero(t, strayed.Load(), "checks ran while no child was running")
}

// TestCheckedAfterTheCheck pins that the Checked event of a passing check
// comes only once the check has returned, so a consumer never reports a
// check as passed before it ran.
func TestCheckedAfterTheCheck(t *testing.T) {
	dir := t.TempDir()

	var (
		lock     sync.Mutex
		finished []time.Time
	)

	rec := &recorder{}
	sup := newHarness(t, fixedSource(helperSpec(t, dir, modeServe), false), Options{
		StopSignals: stopSignals,
		CheckPeriod: 10 * time.Millisecond,
		Check: func(context.Context) error {
			time.Sleep(20 * time.Millisecond)
			lock.Lock()

			finished = append(finished, time.Now())

			lock.Unlock()

			return nil
		},
		OnEvent: rec.record,
	})

	sup.rec = rec
	sup.start()

	started := waitEvent[Started](t, rec, 1)
	waitEvent[Checked](t, rec, 3)
	waitReady(t, dir, started.Pid)
	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())

	_, reported := eventsOf[Checked](rec)

	lock.Lock()
	defer lock.Unlock()

	require.GreaterOrEqual(t, len(finished), len(reported))

	for index, at := range reported {
		assert.False(t, at.Before(finished[index]), "check %d reported before it ended", index)
	}
}

// TestCheckPanicsNil pins that a check that panics with nil fails, also
// under GODEBUG=panicnil=1, where recover returns nil for it. GODEBUG is read
// when the process starts, so the check runs in a helper process.
func TestCheckPanicsNil(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)

	for _, godebug := range []string{"", "panicnil=1"} {
		t.Run("GODEBUG="+godebug, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), exe)

			cmd.Env = append(os.Environ(), helperEnv+"="+modePanicNil, "GODEBUG="+godebug)

			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "the check passed: %s", out)
			assert.Contains(t, string(out), ErrCheckPanicked.Error())
		})
	}
}

// TestOnlyCancelled pins which check results say nothing but that the check
// was cancelled: only those whose every leaf is context.Canceled.
func TestOnlyCancelled(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "the cancellation", err: context.Canceled, want: true},
		{name: "wrapped", err: fmt.Errorf("check: %w", context.Canceled), want: true},
		{
			name: "joined cancellations",
			err:  errors.Join(context.Canceled, fmt.Errorf("again: %w", context.Canceled)),
			want: true,
		},
		{name: "a finding", err: errTest, want: false},
		{name: "joined with a finding", err: errors.Join(errTest, context.Canceled), want: false},
		{
			name: "two %w with a finding",
			err:  fmt.Errorf("%w: %w", errTest, context.Canceled),
			want: false,
		},
		{
			name: "a finding deep in the tree",
			err: fmt.Errorf("outer: %w", errors.Join(context.Canceled,
				fmt.Errorf("inner: %w", errTest))),
			want: false,
		},
		{name: "the deadline", err: context.DeadlineExceeded, want: false},
		{name: "an empty join", err: emptyJoinError{}, want: false},
	}

	for _, test := range cases {
		assert.Equal(t, test.want, onlyCancelled(test.err), test.name)
	}
}

// emptyJoinError is an error joining nothing.
type emptyJoinError struct{}

func (emptyJoinError) Error() string   { return "empty" }
func (emptyJoinError) Unwrap() []error { return nil }
