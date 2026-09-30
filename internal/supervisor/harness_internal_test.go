package supervisor

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// waitTimeout bounds every wait for something that should happen soon.
	waitTimeout = 20 * time.Second
	// pollInterval is how often a wait looks again.
	pollInterval = 5 * time.Millisecond
	// longStopTimeout is a stop timeout no graceful test reaches.
	longStopTimeout = 10 * time.Second
)

// helperSpec is the Spec of a helper child in mode that writes into dir.
func helperSpec(t *testing.T, dir, mode string, env ...string) Spec {
	t.Helper()

	exe, err := os.Executable()
	require.NoError(t, err)

	own := []string{
		helperEnv + "=" + mode,
		dirEnv + "=" + dir,
		// The race runtime otherwise lingers a second in every exit(0).
		"GORACE=atexit_sleep_ms=0",
	}

	return Spec{
		Path:              exe,
		Env:               append(append(os.Environ(), own...), env...),
		StopSignal:        syscall.SIGTERM,
		ForwardStopSignal: false,
		StopTimeout:       longStopTimeout,
	}
}

// stamped is an event with the time it was emitted.
type stamped struct {
	event Event
	at    time.Time
}

// recorder keeps every event of an engine.
type recorder struct {
	mu     sync.Mutex
	events []stamped
}

func (rec *recorder) record(event Event) {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	rec.events = append(rec.events, stamped{event: event, at: time.Now()})
}

func (rec *recorder) all() []stamped {
	rec.mu.Lock()
	defer rec.mu.Unlock()

	return slices.Clone(rec.events)
}

// eventsOf returns the recorded events of type T with their times.
func eventsOf[T Event](rec *recorder) ([]T, []time.Time) {
	var (
		events []T
		times  []time.Time
	)

	for _, item := range rec.all() {
		event, ok := item.event.(T)
		if ok {
			events = append(events, event)
			times = append(times, item.at)
		}
	}

	return events, times
}

// waitEvent waits for the nth (1-based) event of type T and returns it.
func waitEvent[T Event](t *testing.T, rec *recorder, nth int) T {
	t.Helper()

	var found T

	require.Eventually(t, func() bool {
		events, _ := eventsOf[T](rec)
		if len(events) < nth {
			return false
		}

		found = events[nth-1]

		return true
	}, waitTimeout, pollInterval, "no event #%d of type %T", nth, found)

	return found
}

// testSource hands out Specs from next and restart decisions from restart,
// counting the calls. n is the 1-based number of the call.
type testSource struct {
	mu       sync.Mutex
	next     func(ctx context.Context, n int) (Spec, error)
	restart  func(n int, exit Exit) (bool, error)
	nexts    int
	restarts int
}

func (src *testSource) Next(ctx context.Context) (Spec, error) {
	return src.next(ctx, src.count(&src.nexts))
}

func (src *testSource) Restart(_ context.Context, exit Exit) (bool, error) {
	return src.restart(src.count(&src.restarts), exit)
}

// count increments one of the call counters and returns its new value.
func (src *testSource) count(counter *int) int {
	src.mu.Lock()
	defer src.mu.Unlock()

	*counter++

	return *counter
}

func (src *testSource) calls() (int, int) {
	src.mu.Lock()
	defer src.mu.Unlock()

	return src.nexts, src.restarts
}

// fixedSource starts spec every time and restarts while restart says so.
func fixedSource(spec Spec, restart bool) *testSource {
	return &testSource{
		next: func(context.Context, int) (Spec, error) { return spec, nil },
		restart: func(int, Exit) (bool, error) {
			return restart, nil
		},
	}
}

// harness runs an engine whose signals the test injects.
type harness struct {
	t       *testing.T
	engine  *Engine
	rec     *recorder
	signals chan os.Signal
	cancel  context.CancelFunc
	done    chan error
	once    sync.Once
	result  error
}

// newHarness creates the engine. Unless opts has its own OnEvent, the
// harness records the events.
func newHarness(t *testing.T, src Source, opts Options) *harness {
	t.Helper()

	rec := &recorder{}
	if opts.OnEvent == nil {
		opts.OnEvent = rec.record
	}

	engine, err := New(src, opts)
	require.NoError(t, err)

	signals := make(chan os.Signal, 1<<14)

	engine.subscribe = func() (<-chan os.Signal, func()) { return signals, func() {} }

	return &harness{
		t:       t,
		engine:  engine,
		rec:     rec,
		signals: signals,
		done:    make(chan error, 1),
	}
}

// start runs the engine on a goroutine. At the end of the test the engine is
// stopped if it still runs, and every child it started is killed if alive.
func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())

	h.cancel = cancel

	go func() { h.done <- h.engine.Run(ctx) }()

	h.t.Cleanup(func() {
		cancel()
		h.once.Do(func() {
			select {
			case h.result = <-h.done:
			case <-time.After(waitTimeout):
				h.t.Error("Run did not return after the test")
			}
		})

		// Only children never reported exited: a reaped pid may already
		// belong to an unrelated process.
		for _, pid := range h.unreaped() {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

// unreaped are the pids of the children started and not reported exited.
func (h *harness) unreaped() []int {
	var pids []int

	for _, item := range h.rec.all() {
		switch event := item.event.(type) {
		case Started:
			pids = append(pids, event.Pid)
		case Exit:
			pids = slices.DeleteFunc(pids, func(pid int) bool { return pid == event.Pid })
		}
	}

	return pids
}

// wait returns the result of Run, failing the test if it takes too long.
func (h *harness) wait() error {
	h.t.Helper()

	h.once.Do(func() {
		select {
		case h.result = <-h.done:
		case <-time.After(waitTimeout):
			require.FailNow(h.t, "Run did not return")
		}
	})

	return h.result
}

// send injects signals as if the process had received them.
func (h *harness) send(sigs ...syscall.Signal) {
	for _, sig := range sigs {
		h.signals <- sig
	}
}

// pids are the pids of every child that was started or exited.
func (h *harness) pids() []int {
	var pids []int

	for _, item := range h.rec.all() {
		switch event := item.event.(type) {
		case Started:
			pids = append(pids, event.Pid)
		case Exit:
			pids = append(pids, event.Pid)
		}
	}

	slices.Sort(pids)

	return slices.Compact(pids)
}

// assertNoChildren checks that no child the engine started is still around,
// not even as a zombie: the engine has waited for each of them.
func (h *harness) assertNoChildren() {
	h.t.Helper()

	for _, pid := range h.pids() {
		assert.ErrorIs(h.t, syscall.Kill(pid, 0), syscall.ESRCH, "child %d is still around", pid)
	}
}

// waitFile waits for path to exist and returns its content.
func waitFile(t *testing.T, path string) string {
	t.Helper()

	var content []byte

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}

		content = data

		return true
	}, waitTimeout, pollInterval, "%s did not appear", path)

	return string(content)
}

// waitReady waits for the helper child pid to be ready for signals.
func waitReady(t *testing.T, dir string, pid int) {
	t.Helper()
	waitFile(t, helperFile(dir, "ready", pid))
}

// waitSignals waits until the helper pid has recorded every one of want.
func waitSignals(t *testing.T, dir string, pid int, want ...syscall.Signal) {
	t.Helper()

	path := helperFile(dir, "signals", pid)

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}

		// Only whole lines count: a line being written is not a record yet.
		got := strings.Split(string(data), "\n")
		for _, sig := range want {
			if !slices.Contains(got[:len(got)-1], sig.String()) {
				return false
			}
		}

		return true
	}, waitTimeout, pollInterval, "%d did not record %v", pid, want)
}

// termSignal is the signal that ended the process in state, or 0.
func termSignal(state *os.ProcessState) syscall.Signal {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return 0
	}

	return status.Signal()
}

// readPid reads a pid file, checking its exact format.
func readPid(t *testing.T, path string) int {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err, "the pid file holds %q", data)

	return pid
}

// deadPid returns the pid of a process that has exited and been waited for.
func deadPid(t *testing.T) int {
	t.Helper()

	dir := t.TempDir()
	spec := helperSpec(t, dir, modeExit, codeEnv+"=0")
	cmd := command(t.Context(), &spec)
	require.NoError(t, cmd.Run())

	return cmd.Process.Pid
}

// errorOp returns the Op of the *Error in err.
func errorOp(t *testing.T, err error) Op {
	t.Helper()

	var runErr *Error

	require.ErrorAs(t, err, &runErr)

	return runErr.Op
}

var (
	errTest = errors.New("test failure")
	// errPanic is what the panicking callbacks panic with.
	errPanic = errors.New("callback panic")
)

// stopSignals are the stop signals of most tests.
var stopSignals = []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}

// waitReaped waits until pid is gone for good. A zombie still answers
// signal 0; only a waited-for process does not.
func waitReaped(t *testing.T, pid int) {
	t.Helper()

	require.Eventually(t, func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	}, waitTimeout, time.Millisecond, "%d was not waited for", pid)
}

// waitPid waits for a pid file to hold a pid and returns it.
func waitPid(t *testing.T, path string) int {
	t.Helper()

	var pid int

	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}

		pid, err = strconv.Atoi(string(data))

		return err == nil
	}, waitTimeout, pollInterval, "%s holds no pid", path)

	return pid
}
