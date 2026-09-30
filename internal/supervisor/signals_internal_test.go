package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardSignals pins that the reload signal runs the hook and reaches
// the child, and that any other signal reaches the child.
func TestForwardSignals(t *testing.T) {
	dir := t.TempDir()

	var reloads atomic.Int32

	sup := newHarness(t, fixedSource(helperSpec(t, dir, modeServe), false), Options{
		StopSignals:  stopSignals,
		ReloadSignal: syscall.SIGHUP,
		OnReload: func() error {
			reloads.Add(1)

			return nil
		},
	})
	sup.start()

	started := waitEvent[Started](t, sup.rec, 1)
	waitReady(t, dir, started.Pid)
	sup.send(syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGUSR2)
	waitSignals(t, dir, started.Pid, syscall.SIGHUP, syscall.SIGUSR1, syscall.SIGUSR2)
	assert.Equal(t, int32(1), reloads.Load())

	sup.send(syscall.SIGTERM)
	require.NoError(t, sup.wait())

	got, _ := eventsOf[SignalReceived](sup.rec)
	require.Len(t, got, 4)
	assert.Equal(t, SignalReceived{Signal: syscall.SIGHUP, Action: ActionReload}, got[0])
	assert.Equal(t, SignalReceived{Signal: syscall.SIGUSR1, Action: ActionForward}, got[1])
	assert.Equal(t, SignalReceived{Signal: syscall.SIGUSR2, Action: ActionForward}, got[2])
}

// TestIgnoreSignals pins the ignore set: an ignored signal is dropped with
// an event, never forwarded and never stopping anything, with a child and
// without one.
func TestIgnoreSignals(t *testing.T) {
	dir := t.TempDir()
	src := fixedSource(helperSpec(t, dir, modeServe), true)

	src.restart = func(n int, _ Exit) (bool, error) { return n < 2, nil }

	sup := newHarness(t, src, Options{
		StopSignals:   stopSignals,
		IgnoreSignals: []syscall.Signal{syscall.SIGUSR2, syscall.SIGHUP},
		RestartDelay:  time.Hour,
	})
	sup.start()

	first := waitEvent[Started](t, sup.rec, 1)
	waitReady(t, dir, first.Pid)

	// The ignored signals go first; a forwarded one after them marks when
	// they would have arrived.
	sup.send(syscall.SIGUSR2, syscall.SIGHUP, syscall.SIGUSR1)
	waitSignals(t, dir, first.Pid, syscall.SIGUSR1)

	recorded, err := os.ReadFile(helperFile(dir, "signals", first.Pid))
	require.NoError(t, err)
	assert.Equal(t, syscall.SIGUSR1.String(), strings.TrimSpace(string(recorded)))

	// Without a child: the child exits, the restart delay runs.
	require.NoError(t, syscall.Kill(first.Pid, syscall.SIGKILL))
	waitEvent[Restarting](t, sup.rec, 1)
	sup.send(syscall.SIGHUP, syscall.SIGUSR2, syscall.SIGTERM)
	require.NoError(t, sup.wait())

	got, _ := eventsOf[SignalReceived](sup.rec)
	assert.Equal(t, []SignalReceived{
		{Signal: syscall.SIGUSR2, Action: ActionIgnore},
		{Signal: syscall.SIGHUP, Action: ActionIgnore},
		{Signal: syscall.SIGUSR1, Action: ActionForward},
		{Signal: syscall.SIGHUP, Action: ActionIgnore},
		{Signal: syscall.SIGUSR2, Action: ActionIgnore},
		{Signal: syscall.SIGTERM, Action: ActionStop},
	}, got)
}

// TestSignalsDuringStartup pins that signals arriving while the Source
// prepares the child are handled, in order, before the child would start,
// as while no child runs: the reload hook runs and a stop means the child is
// never started.
func TestSignalsDuringStartup(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	src := fixedSource(helperSpec(t, dir, modeServe), true)

	src.next = func(_ context.Context, n int) (Spec, error) {
		if n == 1 {
			close(entered)
			<-release
		}

		return helperSpec(t, dir, modeServe), nil
	}

	var reloads atomic.Int32

	sup := newHarness(t, src, Options{
		StopSignals:  stopSignals,
		ReloadSignal: syscall.SIGHUP,
		OnReload: func() error {
			reloads.Add(1)

			return nil
		},
	})
	sup.start()

	<-entered
	sup.send(syscall.SIGHUP, syscall.SIGTERM)
	close(release)
	require.NoError(t, sup.wait())

	assert.Equal(t, int32(1), reloads.Load())

	var order []string

	for _, item := range sup.rec.all() {
		switch event := item.event.(type) {
		case Started:
			order = append(order, "started")
		case SignalReceived:
			order = append(order, event.Signal.String()+" "+event.Action.String())
		case Exit:
			order = append(order, "exit")
		}
	}

	assert.Equal(t, []string{
		syscall.SIGHUP.String() + " reload",
		syscall.SIGTERM.String() + " stop",
	}, order)

	nexts, restarts := src.calls()
	assert.Equal(t, 1, nexts)
	assert.Zero(t, restarts)
	sup.assertNoChildren()
}

// TestSignalsDuringRestartDelay pins the handling of signals while no child
// runs: the reload hook runs, anything to forward is dropped.
func TestSignalsDuringRestartDelay(t *testing.T) {
	dir := t.TempDir()
	src := fixedSource(helperSpec(t, dir, modeExit, codeEnv+"=1"), true)

	var reloads atomic.Int32

	sup := newHarness(t, src, Options{
		StopSignals:  stopSignals,
		RestartDelay: time.Hour,
		ReloadSignal: syscall.SIGHUP,
		OnReload: func() error {
			reloads.Add(1)

			return errTest
		},
	})
	sup.start()

	waitEvent[Restarting](t, sup.rec, 1)
	sup.send(syscall.SIGUSR1, syscall.SIGHUP, syscall.SIGTERM)
	require.NoError(t, sup.wait())

	got, _ := eventsOf[SignalReceived](sup.rec)
	require.Len(t, got, 3)
	assert.Equal(t, SignalReceived{Signal: syscall.SIGUSR1, Action: ActionDrop}, got[0])
	assert.Equal(t, SignalReceived{Signal: syscall.SIGHUP, Action: ActionReload, Err: errTest},
		got[1])
	assert.Equal(t, SignalReceived{Signal: syscall.SIGTERM, Action: ActionStop}, got[2])
	assert.Equal(t, int32(1), reloads.Load())

	nexts, _ := src.calls()
	assert.Equal(t, 1, nexts)
}

// TestSignalStormAcrossRestarts sends bursts of signals from several
// goroutines, round after round, alternately while a child runs and while
// none does. It pins that every signal the engine receives is handled
// exactly once and in its right place: forwarded, with the reload hook run
// for each reload signal, while a child runs, and then only to that child;
// dropped, reload hook still run, while none runs; and a stop, sent last,
// still ends Run.
//
// The phases do not depend on timing. In a running phase the child stays up
// until the test kills it after the burst is handled; in an idle phase the
// Source holds the next start until the burst is sent, and the engine takes
// the signals that came before the start as with no child.
//
// Injected, every signal sent reaches the engine, so the counts are exact.
// Through the relay the engine runs on, a signal is sent the way the Go
// runtime delivers one, dropped when the relay's input is full, with the
// runtime's own SIGURG and SIGCHLD mixed in; then the counts are only
// bounded by what was accepted.
func TestSignalStormAcrossRestarts(t *testing.T) {
	t.Run("injected", func(t *testing.T) { signalStorm(t, false) })
	t.Run("relay", func(t *testing.T) { signalStorm(t, true) })
}

func signalStorm(t *testing.T, viaRelay bool) {
	t.Helper()

	const (
		rounds  = 3
		senders = 8
		// perSender keeps a burst below the buffers of the relay, so that
		// only its input, not its output, can drop a signal.
		perSender = 12
		reloadsIn = 4 // One signal in reloadsIn is the reload signal.
		// Per burst: the reload signals, and the signals to forward.
		burstReloads  = senders * ((perSender + reloadsIn - 1) / reloadsIn)
		burstForwards = senders*perSender - burstReloads
	)

	dir := t.TempDir()
	src := fixedSource(helperSpec(t, dir, modeServe), true)
	entered := make(chan struct{})
	release := make(chan struct{})

	// Every start but the first waits for the idle burst to be sent.
	src.next = func(ctx context.Context, n int) (Spec, error) {
		if n > 1 {
			entered <- struct{}{}

			select {
			case <-release:
			case <-ctx.Done():
			}
		}

		return helperSpec(t, dir, modeServe), nil
	}

	var reloads, accepted atomic.Int32

	sup := newHarness(t, src, Options{
		StopSignals:  stopSignals,
		ReloadSignal: syscall.SIGHUP,
		OnReload: func() error {
			reloads.Add(1)

			return nil
		},
	})

	send := func(sig syscall.Signal) {
		sup.send(sig)
		accepted.Add(1)
	}

	// queued is what waits for the engine to read it.
	queued := func() int { return len(sup.signals) }

	if viaRelay {
		raw := make(chan os.Signal, signalBuffer)
		relayed := make(chan (<-chan os.Signal), 1)

		sup.engine.subscribe = func() (<-chan os.Signal, func()) {
			out, stop := relay(raw, func() {})
			relayed <- out

			return out, stop
		}
		queued = func() int {
			out := <-relayed
			relayed <- out

			return len(out)
		}
		// Like the runtime: never block, drop what does not fit.
		send = func(sig syscall.Signal) {
			select {
			case raw <- sig:
				if sig != syscall.SIGURG && sig != syscall.SIGCHLD {
					accepted.Add(1)
				}
			default:
			}
		}
	}

	burst := func() {
		var group sync.WaitGroup

		for range senders {
			group.Go(func() {
				for index := range perSender {
					sig := syscall.SIGUSR1
					if index%reloadsIn == 0 {
						sig = syscall.SIGHUP
					}

					send(sig)

					if viaRelay && index%reloadsIn == 1 {
						send(syscall.SIGURG)
						send(syscall.SIGCHLD)
					}
				}
			})
		}

		group.Wait()
	}

	waitHandled := func() {
		require.Eventually(t, func() bool {
			handled, _ := eventsOf[SignalReceived](sup.rec)

			return len(handled) == int(accepted.Load())
		}, waitTimeout, pollInterval, "the burst was not handled")
	}

	sup.start()

	for round := 1; round <= rounds; round++ {
		started := waitEvent[Started](t, sup.rec, round)
		waitReady(t, dir, started.Pid)

		burst()
		waitHandled()

		require.NoError(t, syscall.Kill(started.Pid, syscall.SIGKILL))
		<-entered

		before := accepted.Load()

		burst()

		// The whole burst has to wait for the engine, not be on its way
		// through the relay, when the start goes ahead: a signal that
		// arrives while the child starts goes to the child.
		require.Eventually(t, func() bool {
			return queued() == int(accepted.Load()-before)
		}, waitTimeout, pollInterval, "the burst did not reach the engine")

		release <- struct{}{}

		waitHandled()
	}

	last := waitEvent[Started](t, sup.rec, rounds+1)
	waitReady(t, dir, last.Pid)

	// A stop sent through the relay can be dropped as well: resend it
	// until Run returns, as a user would.
	for stopped := false; !stopped; {
		send(syscall.SIGTERM)

		select {
		case err := <-sup.done:
			sup.done <- err

			stopped = true
		case <-time.After(10 * time.Millisecond):
		}
	}

	require.NoError(t, sup.wait())

	var (
		running bool
		current int
		actions = map[SignalAction]int{}
	)

	for _, item := range sup.rec.all() {
		switch event := item.event.(type) {
		case Started:
			running, current = true, event.Pid
		case Exit:
			require.Equal(t, current, event.Pid)

			running = false
		case SignalReceived:
			actions[event.Action]++

			require.NotContains(t, []syscall.Signal{syscall.SIGURG, syscall.SIGCHLD},
				event.Signal, "the relay passed a runtime signal on")

			switch event.Action {
			case ActionForward:
				require.True(t, running, "a signal forwarded while no child ran")
				require.NoError(t, event.Err, "the child to forward to was gone")
			case ActionDrop:
				require.False(t, running, "a signal dropped while a child ran")
				require.NoError(t, event.Err)
			case ActionReload, ActionStop, ActionIgnore:
			}
		}
	}

	forwards, drops := actions[ActionForward], actions[ActionDrop]
	handled := forwards + drops + actions[ActionReload] + actions[ActionStop]
	assert.Equal(t, actions[ActionReload], int(reloads.Load()))
	assert.Equal(t, int(accepted.Load()), handled)

	if viaRelay {
		assert.GreaterOrEqual(t, actions[ActionStop], 1)
		// Both phases have to be there for the test to test anything.
		assert.Positive(t, forwards)
		assert.Positive(t, drops)
	} else {
		assert.Equal(t, 1, actions[ActionStop])
		assert.Equal(t, rounds*burstForwards, forwards)
		assert.Equal(t, rounds*burstForwards, drops)
		assert.Equal(t, 2*rounds*burstReloads, actions[ActionReload])
	}

	nexts, restarts := src.calls()
	assert.Equal(t, rounds+1, nexts)
	assert.Equal(t, rounds, restarts)
	t.Logf("%d starts, %d accepted, %d handled: %d forwarded, %d dropped",
		nexts, accepted.Load(), handled, forwards, drops)
	sup.assertNoChildren()
}

// TestRealSignals runs the engine in a separate process that takes real
// signals: it pins the OS subscription together with the pid files, the
// reload hook, forwarding and a stop, end to end.
func TestRealSignals(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "supervisor.pid")
	childPidFile := filepath.Join(dir, "child.pid")

	exe, err := os.Executable()
	require.NoError(t, err)

	cmd := command(t.Context(), &Spec{
		Path: exe,
		Env: append(os.Environ(),
			helperEnv+"="+modeSupervise,
			dirEnv+"="+dir,
			pidFileEnv+"="+pidFile,
			childPidFileEnv+"="+childPidFile,
			"GORACE=atexit_sleep_ms=0"),
		StopSignal:  syscall.SIGTERM,
		StopTimeout: longStopTimeout,
	})
	require.NoError(t, cmd.Start())

	supervisor := cmd.Process.Pid
	exited := make(chan error, 1)

	go func() { exited <- cmd.Wait() }()

	childPid := waitPid(t, childPidFile)

	t.Cleanup(func() {
		_ = syscall.Kill(supervisor, syscall.SIGKILL)
		_ = syscall.Kill(childPid, syscall.SIGKILL)

		<-exited
	})

	assert.Equal(t, supervisor, waitPid(t, pidFile))
	waitReady(t, dir, childPid)

	require.NoError(t, syscall.Kill(supervisor, syscall.SIGHUP))
	waitFile(t, filepath.Join(dir, "reloaded"))
	waitSignals(t, dir, childPid, syscall.SIGHUP)

	require.NoError(t, syscall.Kill(supervisor, syscall.SIGUSR1))
	waitSignals(t, dir, childPid, syscall.SIGUSR1)

	require.NoError(t, syscall.Kill(supervisor, syscall.SIGINT))

	select {
	case err := <-exited:
		exited <- err

		require.NoError(t, err)
	case <-time.After(waitTimeout):
		require.FailNow(t, "the supervisor did not exit")
	}

	assert.Equal(t, strconv.Itoa(exitOnInt), waitFile(t, filepath.Join(dir, "exit")))
	assert.NoFileExists(t, pidFile)
	assert.NoFileExists(t, childPidFile)
	assert.ErrorIs(t, syscall.Kill(childPid, 0), syscall.ESRCH)
}

// TestSubscribeOS pins that the real subscription relays signals but not
// the runtime's SIGURG or SIGCHLD.
func TestSubscribeOS(t *testing.T) {
	signals, unsubscribe := subscribeOS()
	defer unsubscribe()

	self := os.Getpid()
	require.NoError(t, syscall.Kill(self, syscall.SIGURG))
	require.NoError(t, syscall.Kill(self, syscall.SIGCHLD))
	require.NoError(t, syscall.Kill(self, syscall.SIGUSR1))

	select {
	case sig := <-signals:
		assert.Equal(t, syscall.SIGUSR1, sig)
	case <-time.After(waitTimeout):
		require.FailNow(t, "no signal relayed")
	}

	select {
	case sig := <-signals:
		assert.Failf(t, "unexpected signal", "%v", sig)
	case <-time.After(100 * time.Millisecond):
	}
}
