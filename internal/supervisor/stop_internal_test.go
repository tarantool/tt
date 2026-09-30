package supervisor

import (
	"context"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStopSignal pins which signal a stop sends to the child: the received
// one with ForwardStopSignal, Spec.StopSignal otherwise. A stopped child is
// not restarted even though the Source would restart it.
func TestStopSignal(t *testing.T) {
	cases := []struct {
		name     string
		forward  bool
		received syscall.Signal
		code     int
	}{
		{name: "forward SIGINT", forward: true, received: syscall.SIGINT, code: exitOnInt},
		{name: "forward SIGQUIT", forward: true, received: syscall.SIGQUIT, code: exitOnQuit},
		{name: "translate SIGINT", forward: false, received: syscall.SIGINT, code: exitOnTerm},
		{name: "translate SIGQUIT", forward: false, received: syscall.SIGQUIT, code: exitOnTerm},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			spec := helperSpec(t, dir, modeServe)

			spec.ForwardStopSignal = test.forward

			src := fixedSource(spec, true)
			sup := newHarness(t, src, Options{StopSignals: stopSignals})
			sup.start()

			started := waitEvent[Started](t, sup.rec, 1)
			waitReady(t, dir, started.Pid)
			sup.send(test.received)

			require.NoError(t, sup.wait())

			exits, _ := eventsOf[Exit](sup.rec)
			require.Len(t, exits, 1)
			assert.Equal(t, test.code, exits[0].State.ExitCode())

			kills, _ := eventsOf[Killed](sup.rec)
			assert.Empty(t, kills)

			nexts, restarts := src.calls()
			assert.Equal(t, 1, nexts)
			assert.Zero(t, restarts)
			sup.assertNoChildren()
		})
	}
}

// TestStopEscalatesToKill pins the SIGKILL sent to a child that outlives the
// stop timeout.
func TestStopEscalatesToKill(t *testing.T) {
	const stopTimeout = 300 * time.Millisecond

	dir := t.TempDir()
	spec := helperSpec(t, dir, modeStubborn)

	spec.StopTimeout = stopTimeout

	sup := newHarness(t, fixedSource(spec, true), Options{StopSignals: stopSignals})
	sup.start()

	started := waitEvent[Started](t, sup.rec, 1)
	waitReady(t, dir, started.Pid)

	stopAt := time.Now()

	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())

	kills, killTimes := eventsOf[Killed](sup.rec)
	require.Len(t, kills, 1)
	assert.Equal(t, KillStopTimeout, kills[0].Reason)
	require.NoError(t, kills[0].Err)
	assert.GreaterOrEqual(t, killTimes[0].Sub(stopAt), stopTimeout)

	exits, _ := eventsOf[Exit](sup.rec)
	require.Len(t, exits, 1)
	assert.Equal(t, syscall.SIGKILL, termSignal(exits[0].State))
	sup.assertNoChildren()
}

// TestRepeatedStopKeepsTimeout pins that a second stop signal reaches the
// child but does not push the SIGKILL back.
func TestRepeatedStopKeepsTimeout(t *testing.T) {
	const (
		stopTimeout = time.Second
		secondAfter = stopTimeout / 2
	)

	dir := t.TempDir()
	spec := helperSpec(t, dir, modeStubborn)

	spec.StopTimeout = stopTimeout

	var (
		sup  *harness
		sent atomic.Bool
	)

	rec := &recorder{}

	sup = newHarness(t, fixedSource(spec, true), Options{
		StopSignals: stopSignals,
		OnEvent: func(event Event) {
			rec.record(event)

			// The loop sends itself the second stop while it handles the
			// first, so the second is always handled well before the kill
			// timer can fire, however the scheduler treats the test.
			received, ok := event.(SignalReceived)
			if ok && received.Action == ActionStop && !sent.Swap(true) {
				time.Sleep(secondAfter)
				sup.send(syscall.SIGINT)
			}
		},
	})

	sup.rec = rec
	sup.start()

	started := waitEvent[Started](t, rec, 1)
	waitReady(t, dir, started.Pid)
	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())

	stops, stopTimes := eventsOf[SignalReceived](rec)
	require.Len(t, stops, 2)

	for _, stop := range stops {
		assert.Equal(t, ActionStop, stop.Action)
		require.NoError(t, stop.Err)
	}

	kills, killTimes := eventsOf[Killed](rec)
	require.Len(t, kills, 1)
	require.True(t, stopTimes[1].Before(killTimes[0]))

	// Kept, the kill comes a stop timeout after the first stop; re-armed, it
	// would come half a timeout later.
	sinceFirst := killTimes[0].Sub(stopTimes[0])
	assert.GreaterOrEqual(t, sinceFirst, stopTimeout-secondAfter/10)
	assert.Less(t, sinceFirst, stopTimeout+secondAfter/2)
}

// TestStopDuringRestartDelay pins that a stop signal or a cancelled context
// ends the restart delay at once and no child is started again.
func TestStopDuringRestartDelay(t *testing.T) {
	cases := map[string]bool{"stop signal": true, "cancelled context": false}

	for name, viaSignal := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := fixedSource(helperSpec(t, dir, modeExit, codeEnv+"=1"), true)
			sup := newHarness(t, src, Options{StopSignals: stopSignals, RestartDelay: time.Hour})
			sup.start()

			waitEvent[Restarting](t, sup.rec, 1)

			if viaSignal {
				sup.send(syscall.SIGINT)
			} else {
				sup.cancel()
			}

			require.NoError(t, sup.wait())

			nexts, restarts := src.calls()
			assert.Equal(t, 1, nexts)
			assert.Equal(t, 1, restarts)
			sup.assertNoChildren()

			if viaSignal {
				stop := waitEvent[SignalReceived](t, sup.rec, 1)
				assert.Equal(t, ActionStop, stop.Action)
				assert.Equal(t, syscall.SIGINT, stop.Signal)
			}
		})
	}
}

// TestStopWhileSourcePrepares pins that a stop that arrives while the Source
// prepares the next child is honoured before that child is started.
func TestStopWhileSourcePrepares(t *testing.T) {
	dir := t.TempDir()
	spec := helperSpec(t, dir, modeExit, codeEnv+"=1")
	src := fixedSource(spec, true)

	var sup *harness

	src.next = func(_ context.Context, n int) (Spec, error) {
		if n == 2 {
			sup.send(syscall.SIGTERM)
		}

		return spec, nil
	}

	sup = newHarness(t, src, Options{StopSignals: stopSignals})
	sup.start()
	require.NoError(t, sup.wait())

	started, _ := eventsOf[Started](sup.rec)
	assert.Len(t, started, 1, "a child started after the stop was queued")

	nexts, _ := src.calls()
	assert.Equal(t, 2, nexts)
}

// TestQueuedStopBeatsRestart repeats the scenario where a stop is queued
// while the child exits, or as the restart delay begins, with no delay at
// all: the engine finds the stop ready together with the exit or with the
// expired timer. The stop must win every time: no child may start after it
// was queued. A choice between the two left to chance loses at least one
// round in four, so forty rounds miss it with a chance below 1e-5.
func TestQueuedStopBeatsRestart(t *testing.T) {
	const rounds = 40

	cases := []struct {
		name string
		// queue sends the stop at the moment of the scenario; it returns
		// once both the stop and the competing event are ready.
		queue func(t *testing.T, sup *harness, event Event) bool
	}{
		{
			name: "child exit",
			queue: func(t *testing.T, sup *harness, event Event) bool {
				t.Helper()

				started, ok := event.(Started)
				if !ok {
					return false
				}

				sup.send(syscall.SIGTERM)
				// Once it is reaped, its exit waits for the engine.
				waitReaped(t, started.Pid)

				return true
			},
		},
		{
			name: "restart delay",
			queue: func(t *testing.T, sup *harness, event Event) bool {
				t.Helper()

				_, ok := event.(Restarting)
				if !ok {
					return false
				}

				sup.send(syscall.SIGTERM)

				return true
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for round := range rounds {
				dir := t.TempDir()
				src := fixedSource(helperSpec(t, dir, modeExit, codeEnv+"=1"), true)
				rec := &recorder{}

				var (
					sup    *harness
					queued atomic.Bool
				)

				sup = newHarness(t, src, Options{
					StopSignals: stopSignals,
					OnEvent: func(event Event) {
						rec.record(event)

						if !queued.Load() && test.queue(t, sup, event) {
							queued.Store(true)
						}
					},
				})

				sup.rec = rec
				sup.start()
				require.NoError(t, sup.wait())

				started, _ := eventsOf[Started](rec)
				require.Len(t, started, 1, "round %d: children started after the stop", round)

				// Nor is the Source asked for anything after the stop: a
				// Spec it failed to prepare would turn a clean stop into an
				// error. A stop queued as the child exits is taken before
				// the Source is asked about a restart.
				nexts, restarts := src.calls()
				require.Equal(t, 1, nexts, "round %d: asked for a Spec after the stop", round)

				if test.name == "child exit" {
					require.Zero(t, restarts, "round %d: asked to restart after the stop", round)
				}
			}
		})
	}
}

// TestStopRacesRestart stops the engine at a different moment in each round
// while a child keeps exiting and restarting, with signals arriving all
// along. Whenever the stop lands - while the child starts, runs, exits or
// waits for its restart - Run returns without another start after it, the
// child is gone and waited for, Cleanup has run once and no pid file is left.
func TestStopRacesRestart(t *testing.T) {
	const (
		rounds = 24
		// stopSpread is the step, in milliseconds, between the stop times
		// of consecutive rounds; the child cycles in about 10ms.
		stopSpread = 7
	)

	for round := range rounds {
		t.Run(strconv.Itoa(round), func(t *testing.T) {
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "supervisor.pid")
			childPidFile := filepath.Join(dir, "child.pid")
			src := fixedSource(helperSpec(t, dir, modeExit, codeEnv+"=1"), true)

			var cleanups atomic.Int32

			sup := newHarness(t, src, Options{
				PidFile:      pidFile,
				ChildPidFile: childPidFile,
				StopSignals:  stopSignals,
				RestartDelay: time.Duration(round%5) * time.Millisecond,
				Cleanup:      func() { cleanups.Add(1) },
			})
			sup.start()

			noise, stopNoise := context.WithCancel(context.Background())
			defer stopNoise()

			go func() {
				for noise.Err() == nil {
					sup.send(syscall.SIGUSR2)
					time.Sleep(time.Millisecond)
				}
			}()

			// Spread the stops over the whole cycle of start, run, exit
			// and delay.
			time.Sleep(time.Duration(round*stopSpread) * time.Millisecond)

			viaSignal := round%2 == 0
			if viaSignal {
				sup.send(syscall.SIGTERM)
			} else {
				sup.cancel()
			}

			// Taken once the stop is sent, so that the test being descheduled
			// cannot count the starts before it as late.
			sentAt := time.Now()

			require.NoError(t, sup.wait())
			stopNoise()

			_, startTimes := eventsOf[Started](sup.rec)

			// A stop that lands while the child is being started lets that
			// one start happen and stops it; no other start may follow.
			assert.LessOrEqual(t, countAfter(startTimes, sentAt), 1,
				"children started after the stop")

			if viaSignal {
				// The stop signal is taken only between starts, so no start
				// follows the moment the engine took it.
				assert.Zero(t, countAfter(startTimes, stopTaken(t, sup.rec)),
					"children started after the stop was taken")
			}

			assert.Equal(t, int32(1), cleanups.Load())
			assert.NoFileExists(t, pidFile)
			assert.NoFileExists(t, childPidFile)
			sup.assertNoChildren()
		})
	}
}

// countAfter counts the times after since.
func countAfter(times []time.Time, since time.Time) int {
	count := 0

	for _, at := range times {
		if at.After(since) {
			count++
		}
	}

	return count
}

// stopTaken is when the engine reported the first stop signal it took.
func stopTaken(t *testing.T, rec *recorder) time.Time {
	t.Helper()

	received, times := eventsOf[SignalReceived](rec)

	for index, event := range received {
		if event.Action == ActionStop {
			return times[index]
		}
	}

	require.FailNow(t, "no stop signal was taken")

	return time.Time{}
}
