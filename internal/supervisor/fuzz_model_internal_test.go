package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeProc is a child the script drives.
type fakeProc struct {
	pid    int
	done   chan Exit
	exited bool
}

// reaped tells that the engine has taken the exit of the child.
func (proc *fakeProc) reaped() bool {
	return proc.exited && len(proc.done) == 0
}

// fuzzRun is one replay of a script.
type fuzzRun struct {
	t        *testing.T
	script   []byte
	cfg      fuzzConfig
	steps    []fuzzStep
	pidFile  string
	childPid string
	clock    *fakeClock
	signals  chan os.Signal
	ctx      context.Context //nolint:containedctx // The run's context is the script's to cancel.
	cancel   context.CancelFunc
	finished chan struct{}
	// completions carries the outcome the script gave the check.
	completions chan error

	// The fields below are guarded by mu.
	mu            sync.Mutex
	procs         []*fakeProc
	sent          []syscall.Signal
	handled       int
	stopRequested bool
	running       int
	failed        map[int]bool
	reported      map[int]bool
	checkFailed   bool
	// checksRunning counts the calls of Check that have not returned, and
	// checkCalls all the calls.
	checksRunning int
	checkCalls    int
	failNext      int
	failStart     int
	restartPlan   []uint8
	used          map[error]bool
	restartSaidNo bool
	inline        [hookCount][]fuzzStep
	panicAt       [hookCount]bool
	cleanups      int
	ownedByEngine bool
	rivalOwned    bool
	result        error
	panicValue    any
	// The effects, recorded where they happen rather than from the events:
	// the signals sent to children since the last event, the calls of
	// OnReload since the last event, and the results the checks returned.
	sends        []fuzzSend
	reloads      int
	checkResults []fuzzCheckResult
	// ctxStopped tells that the stop of a cancelled context, which the
	// engine sends without an event, has been accounted for.
	ctxCancelled bool
	ctxStopped   bool

	violationsMu sync.Mutex
	violations   []string
}

// fuzzSend is a signal the engine sent to a child.
type fuzzSend struct {
	pid int
	sig syscall.Signal
}

// fuzzCheckResult is what a check returned, and whether an event reported it.
type fuzzCheckResult struct {
	err      error
	reported bool
}

// locked runs change under mu.
func (run *fuzzRun) locked(change func()) {
	run.mu.Lock()
	defer run.mu.Unlock()

	change()
}

func (run *fuzzRun) failf(format string, args ...any) {
	run.violationsMu.Lock()
	defer run.violationsMu.Unlock()

	run.violations = append(run.violations, fmt.Sprintf(format, args...))
}

// stopFor tells whether sig is a stop signal.
func stopFor(sig syscall.Signal) bool {
	return slices.Contains(fuzzStops, sig)
}

// inject delivers a signal as the relay would. Under mu, so that the order
// of sent is the order of the channel.
func (run *fuzzRun) inject(sig syscall.Signal) {
	run.mu.Lock()
	defer run.mu.Unlock()

	run.sent = append(run.sent, sig)
	if stopFor(sig) {
		run.stopRequested = true
	}

	run.signals <- sig
}

// spawn is the engine's spawn: a fake child, and the checks that no child
// starts when it must not.
func (run *fuzzRun) spawn(context.Context, *Spec) (*child, error) {
	run.mu.Lock()
	defer run.mu.Unlock()

	if run.failStart > 0 {
		run.failStart--

		run.used[errFuzzStart] = true

		return nil, errFuzzStart
	}

	if run.stopRequested {
		run.failf("a child started after a stop was received")
	}

	if run.checkFailed {
		run.failf("a child started after a failed check")
	}

	for _, proc := range run.procs {
		if !proc.reaped() {
			run.failf("a child started while %d was not yet reaped", proc.pid)
		}
	}

	proc := &fakeProc{pid: fuzzFirstPid + len(run.procs), done: make(chan Exit, 1), exited: false}

	run.procs = append(run.procs, proc)

	return &child{
		pid:  proc.pid,
		done: proc.done,
		send: func(sig syscall.Signal) error { return run.deliver(proc, sig) },
	}, nil
}

// deliver is a signal reaching a fake child.
func (run *fuzzRun) deliver(proc *fakeProc, sig syscall.Signal) error {
	run.mu.Lock()
	defer run.mu.Unlock()

	run.sends = append(run.sends, fuzzSend{pid: proc.pid, sig: sig})

	if proc.exited {
		return fmt.Errorf("signalling %d: %w", proc.pid, os.ErrProcessDone)
	}

	if sig == syscall.SIGKILL || (stopFor(sig) && !run.cfg.stubborn) {
		run.exitLocked(proc)
	}

	return nil
}

func (run *fuzzRun) exitLocked(proc *fakeProc) {
	proc.exited = true
	proc.done <- Exit{Pid: proc.pid, State: nil, Err: nil}
}

// check is Options.Check: it waits for the script's outcome, or for the
// cancellation that comes when the child exits.
func (run *fuzzRun) check(ctx context.Context) error {
	var owner int

	run.locked(func() {
		owner = run.running

		run.checksRunning++

		run.checkCalls++
	})

	defer run.locked(func() { run.checksRunning-- })

	if owner == 0 {
		run.failf("a check ran while no child was running")
	}

	var err error

	select {
	case err = <-run.completions:
	case <-ctx.Done():
		err = fmt.Errorf("check interrupted: %w", ctx.Err())
		if run.cfg.lateFails {
			err = errFuzzCheck
		}
	}

	run.locked(func() {
		run.checkResults = append(run.checkResults, fuzzCheckResult{err: err, reported: false})

		if errors.Is(err, errFuzzCheck) {
			run.failed[owner] = true
			run.checkFailed = true
		}
	})

	return err
}

// hook runs the steps the script put inside a callback, then panics there if
// the script says so.
func (run *fuzzRun) hook(kind fuzzHook) {
	var (
		steps  []fuzzStep
		panics bool
	)

	run.locked(func() {
		steps, run.inline[kind] = run.inline[kind], nil
		panics, run.panicAt[kind] = run.panicAt[kind], false
	})

	for _, step := range steps {
		run.apply(step)
	}

	if panics {
		panic(errFuzzPanic)
	}
}

func (run *fuzzRun) pidFileHolds(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(string(data))
	if err != nil {
		run.failf("%s holds %q", path, data)
	}

	return pid, true
}

// wantAction is what the engine has to do with sig in the current state.
func (run *fuzzRun) wantAction(sig syscall.Signal) SignalAction {
	switch {
	case stopFor(sig):
		return ActionStop
	case sig == fuzzReload:
		return ActionReload
	case sig == fuzzIgnored:
		return ActionIgnore
	case run.running != 0:
		return ActionForward
	default:
		return ActionDrop
	}
}

// onEvent is Options.OnEvent: it follows the events and checks each against
// the model.
func (run *fuzzRun) onEvent(event Event) {
	if os.Getenv("FUZZ_TRACE") != "" {
		fmt.Fprintf(os.Stderr, "TRACE %T %+v\n", event, event)
	}

	run.mu.Lock()

	var kind fuzzHook

	switch event := event.(type) {
	case PidFileWritten:
		kind = hookCount

		run.expectEffects(event, nil, 0)

		if event.Path == run.pidFile {
			run.ownedByEngine = true
		}
	case Started:
		kind = hookStarted

		run.expectEffects(event, nil, 0)
		run.onStarted(event)
	case Exit:
		kind = hookExit

		run.expectEffects(event, nil, 0)
		run.onExit(event)
	case SignalReceived:
		kind = hookSignal

		run.onSignal(event)
	case Checked:
		kind = hookChecked

		run.expectEffects(event, nil, 0)
		run.onChecked(event)
	case Killed:
		kind = hookKilled

		run.expectEffects(event, []fuzzSend{{pid: event.Pid, sig: syscall.SIGKILL}}, 0)

		if event.Pid != run.running {
			run.failf("%d killed while %d is the child", event.Pid, run.running)
		}
	case Restarting:
		kind = hookRestarting

		run.expectEffects(event, nil, 0)
	}

	run.mu.Unlock()

	if kind != hookCount {
		run.hook(kind)
	}
}

func (run *fuzzRun) onStarted(event Started) {
	if run.running != 0 {
		run.failf("%d started while %d runs", event.Pid, run.running)
	}

	run.running = event.Pid

	if run.cfg.childPidFile {
		pid, ok := run.pidFileHolds(run.childPid)
		if !ok || pid != event.Pid {
			run.failf("at the start of %d the child pid file holds %d (present %v)",
				event.Pid, pid, ok)
		}
	}
}

func (run *fuzzRun) onExit(event Exit) {
	if event.Pid != run.running {
		run.failf("%d exited while %d was the child", event.Pid, run.running)
	}

	run.running = 0

	if _, ok := run.pidFileHolds(run.childPid); ok {
		run.failf("the child pid file outlived %d", event.Pid)
	}

	// Every failure of a check of this child has been reported by now. A
	// pass or a bare cancellation that came as it exited is not reported.
	if run.failed[event.Pid] != run.reported[event.Pid] {
		run.failf("the check of %d failed: %v, reported: %v", event.Pid,
			run.failed[event.Pid], run.reported[event.Pid])
	}

	run.checkResults = nil
}

func (run *fuzzRun) onSignal(event SignalReceived) {
	if run.handled >= len(run.sent) {
		run.failf("%s handled but never sent", event.Signal)

		return
	}

	sig := run.sent[run.handled]
	run.handled++

	if event.Signal != sig {
		run.failf("%s handled where %s was sent", event.Signal, sig)
	}

	want := run.wantAction(sig)
	if event.Action != want {
		run.failf("%s handled as %s, want %s (child %d)", sig, event.Action, want, run.running)
	}

	if event.Err != nil && !errors.Is(event.Err, os.ErrProcessDone) {
		run.failf("%s handled with %v", sig, event.Err)
	}

	// What the action has to have done, whatever the event says.
	var (
		sends   []fuzzSend
		reloads int
	)

	switch want {
	case ActionStop:
		if run.running != 0 {
			out := syscall.SIGTERM
			if run.cfg.forwardStop {
				out = sig
			}

			sends = []fuzzSend{{pid: run.running, sig: out}}
		}
	case ActionReload:
		reloads = 1

		if run.running != 0 {
			sends = []fuzzSend{{pid: run.running, sig: sig}}
		}
	case ActionForward:
		sends = []fuzzSend{{pid: run.running, sig: sig}}
	case ActionDrop, ActionIgnore:
	}

	run.expectEffects(event, sends, reloads)
}

// expectEffects checks that since the previous event the engine sent the
// children exactly sends and called OnReload reloads times, and starts
// counting afresh. The stop that a cancelled context sends the child comes
// with no event; it is taken once, from the front.
func (run *fuzzRun) expectEffects(event Event, sends []fuzzSend, reloads int) {
	got := run.sends
	ctxStop := fuzzSend{pid: run.running, sig: syscall.SIGTERM}

	// The stop of the context is told apart from the sends the event
	// accounts for only when the sends do not match without it.
	if !slices.Equal(got, sends) && run.ctxCancelled && !run.ctxStopped &&
		run.running != 0 && len(got) > 0 && got[0] == ctxStop {
		got = got[1:]
		run.ctxStopped = true
	}

	if !slices.Equal(got, sends) {
		run.failf("at %T %+v the engine had sent %v, want %v", event, event, got, sends)
	}

	if run.reloads != reloads {
		run.failf("at %T %+v OnReload had run %d times, want %d", event, event,
			run.reloads, reloads)
	}

	run.sends, run.reloads = nil, 0
}

// onChecked checks a Checked event against the results the checks returned:
// it reports the oldest one not yet reported, as it was.
func (run *fuzzRun) onChecked(event Checked) {
	index := slices.IndexFunc(run.checkResults, func(result fuzzCheckResult) bool {
		return !result.reported
	})
	if index < 0 {
		run.failf("Checked %v with no check that returned", event.Err)

		return
	}

	result := &run.checkResults[index]

	result.reported = true

	if (result.err == nil) != (event.Err == nil) ||
		(result.err != nil && !errors.Is(event.Err, result.err)) {
		run.failf("Checked %v for a check that returned %v", event.Err, result.err)
	}

	switch {
	case event.Err == nil:
	case !errors.Is(event.Err, errFuzzCheck):
		run.failf("a check reported %v", event.Err)
	case run.running == 0:
		run.failf("a failed check reported with no child")
	default:
		run.reported[run.running] = true
	}
}

// next is Source.Next.
func (run *fuzzRun) next(context.Context) (Spec, error) {
	run.mu.Lock()

	if run.stopRequested {
		run.failf("the Source was asked for a Spec after a stop")
	}

	var err error

	if run.failNext > 0 {
		run.failNext--

		run.used[errFuzzNext] = true
		err = errFuzzNext
	}

	run.mu.Unlock()
	run.hook(hookNext)

	spec := Spec{
		Path:              "fuzz",
		Args:              nil,
		Env:               nil,
		Dir:               "",
		Stdin:             nil,
		Stdout:            nil,
		Stderr:            nil,
		StopSignal:        syscall.SIGTERM,
		ForwardStopSignal: run.cfg.forwardStop,
		StopTimeout:       fuzzStopTimeout,
		ProcessGroup:      false,
	}

	return spec, err
}

// restart is Source.Restart.
func (run *fuzzRun) restart(exit Exit) (bool, error) {
	run.mu.Lock()

	if run.stopRequested {
		run.failf("the Source was asked about a restart after a stop")
	}

	// The Source here says yes unless the script says otherwise, so a
	// failed check that reached it would be restarted over.
	if run.checkFailed {
		run.failf("the Source was asked about a restart of %d after a failed check", exit.Pid)
	}

	restart, err := true, error(nil)

	if len(run.restartPlan) > 0 {
		switch run.restartPlan[0] % 3 {
		case 1:
			restart = false
			run.restartSaidNo = true
		case 2:
			restart, err = false, errFuzzRestart
			run.used[errFuzzRestart] = true
		}

		run.restartPlan = run.restartPlan[1:]
	}

	run.mu.Unlock()
	run.hook(hookRestart)

	return restart, err
}

// fuzzSource adapts a fuzzRun to Source.
type fuzzSource struct{ run *fuzzRun }

func (src fuzzSource) Next(ctx context.Context) (Spec, error) { return src.run.next(ctx) }

func (src fuzzSource) Restart(_ context.Context, exit Exit) (bool, error) {
	return src.run.restart(exit)
}

func (run *fuzzRun) cleanup() {
	run.mu.Lock()
	defer run.mu.Unlock()

	run.cleanups++

	for _, proc := range run.procs {
		if !proc.reaped() {
			run.failf("Cleanup ran while %d was not reaped", proc.pid)
		}
	}

	run.checkOwnPidFile("Cleanup")
}

// checkOwnPidFile checks that the supervisor pid file, which the engine owns
// from the event that reports it written until Cleanup has run, names the
// engine all that time: nobody took it over.
func (run *fuzzRun) checkOwnPidFile(when string) {
	if !run.ownedByEngine || run.cleanups > 1 {
		return
	}

	pid, ok := run.pidFileHolds(run.pidFile)
	if !ok || pid != os.Getpid() {
		run.failf("at %s the supervisor pid file holds %d (present %v)", when, pid, ok)
	}
}

// apply performs one step: from the script, or inside a callback.
func (run *fuzzRun) apply(step fuzzStep) {
	switch step.op {
	case opStop:
		run.inject(fuzzStops[int(step.arg)%len(fuzzStops)])
	case opReload:
		run.inject(fuzzReload)
	case opForward:
		run.inject(fuzzForward)
	case opIgnored:
		run.inject(fuzzIgnored)
	case opExit:
		run.locked(func() {
			for _, proc := range run.procs {
				if !proc.exited {
					run.exitLocked(proc)
				}
			}
		})
	case opAdvance:
		run.clock.advance(time.Duration(step.arg%16+1) * fuzzUnit)
	case opCheckPass, opCheckFail:
		outcome := error(nil)
		if step.op == opCheckFail {
			outcome = errFuzzCheck
		}

		select {
		case run.completions <- outcome:
		default:
		}
	case opCancel:
		run.locked(func() {
			run.stopRequested = true
			run.ctxCancelled = true
		})
		run.cancel()
	case opFailNext:
		run.locked(func() { run.failNext++ })
	case opFailStart:
		run.locked(func() { run.failStart++ })
	case opRestartPlan:
		run.locked(func() { run.restartPlan = append(run.restartPlan, step.arg) })
	case opInline, opBatch, opPanic, opCount:
		// Meaningless inside a callback.
	}
}

// engineStacks returns the stacks of the goroutines of the engine: the one
// running Run, recognised by the closure that calls it even before it has
// got that far, and the checker. A goroutine is recognised by a frame of the
// engine, so every goroutine the engine starts must have one from the
// moment it is created, before it first runs: one started through a helper
// such as sync.WaitGroup.Go shows only the helper's frame until then, and
// would pass for no goroutine at all.
func engineStacks() []string {
	buf := make([]byte, 1<<16)

	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]

			break
		}

		buf = make([]byte, 2*len(buf))
	}

	var stacks []string

	for stack := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(stack, "supervisor.(*Engine)") ||
			strings.Contains(stack, "supervisor.(*fuzzRun).drive.func") {
			stacks = append(stacks, stack)
		}
	}

	return stacks
}

// quiet tells whether every goroutine of the engine waits in a select. As
// every channel it waits on is made ready by the script alone, the engine
// then cannot move until the script does. It is called only while Run is
// active, so it also requires that the goroutine running Run is among those
// it recognises: were the stacks to change shape so that it recognised none,
// the engine would never look quiet, rather than always.
func quiet() bool {
	stacks := engineStacks()
	runner := false

	for _, stack := range stacks {
		header, _, _ := strings.Cut(stack, "\n")
		if !strings.Contains(header, "[select") {
			return false
		}

		runner = runner || strings.Contains(stack, "supervisor.(*fuzzRun).drive.func")
	}

	return runner
}

func (run *fuzzRun) isFinished() bool {
	select {
	case <-run.finished:
		return true
	default:
		return false
	}
}

// settle waits until the engine can move no further on its own.
func (run *fuzzRun) settle() {
	deadline := time.Now().Add(fuzzSettleTimeout)

	for spin := 0; ; spin++ {
		if run.isFinished() || quiet() {
			return
		}

		if time.Now().After(deadline) {
			run.t.Fatalf("the engine did not settle, or its goroutines are not "+
				"recognised any more\nscript %s\n%s", run.describe(),
				strings.Join(engineStacks(), "\n\n"))
		}

		if spin < 100 {
			runtime.Gosched()
		} else {
			time.Sleep(20 * time.Microsecond)
		}
	}
}

// checkSettled checks the invariants that hold whenever the engine rests.
func (run *fuzzRun) checkSettled() {
	run.mu.Lock()
	defer run.mu.Unlock()

	alive := 0

	for _, proc := range run.procs {
		if !proc.exited {
			alive++
		}
	}

	if alive > 1 {
		run.failf("%d children alive at once", alive)
	}

	if !run.isFinished() && run.cleanups == 0 {
		run.checkOwnPidFile("a settled step")
	}

	if run.cfg.childPidFile && !run.isFinished() {
		pid, ok := run.pidFileHolds(run.childPid)

		switch {
		case run.running != 0 && (!ok || pid != run.running):
			run.failf("child %d runs, its pid file holds %d (present %v)", run.running, pid, ok)
		case run.running == 0 && ok:
			run.failf("no child runs, its pid file holds %d", pid)
		}
	}

	if !run.isFinished() {
		run.checkCheckPeriods()
	}
}

// checkCheckPeriods accounts for the check periods apart from the checks:
// the fake clock knows every ticker it made. While a child runs with checks
// configured and no check has failed, one check ticker runs, and none while
// no child runs. A tick left waiting in its channel means a period passed
// with no check started, which is allowed only while a check still runs:
// the ticks that come meanwhile coalesce into the next check. And every tick
// a reader took from its channel started one check.
func (run *fuzzRun) checkCheckPeriods() {
	tickers := run.clock.activeTickers()

	if consumed := run.clock.consumedTicks(); consumed != run.checkCalls {
		run.failf("%d check periods were taken and %d checks started", consumed,
			run.checkCalls)
	}

	switch {
	case !run.cfg.checks || run.checkFailed:
		return
	case run.running != 0 && len(tickers) != 1:
		run.failf("child %d runs with %d check tickers", run.running, len(tickers))
	case run.running == 0 && len(tickers) != 0:
		run.failf("%d check tickers run with no child", len(tickers))
	}

	for _, ticker := range tickers {
		if ticker.pending && run.checksRunning == 0 {
			run.failf("a check period passed and no check started (child %d)", run.running)
		}
	}
}

func (run *fuzzRun) describe() string {
	parts := make([]string, 0, len(run.steps))
	for _, step := range run.steps {
		parts = append(parts, step.String())
	}

	return fmt.Sprintf("%+v %s", run.cfg, strings.Join(parts, " "))
}

// drive runs the script against a fresh engine and checks the end state.
func (run *fuzzRun) drive() {
	engine, err := New(fuzzSource{run: run}, Options{
		PidFile:       run.pidFile,
		ChildPidFile:  childPidFileOf(run),
		StopSignals:   fuzzStops,
		IgnoreSignals: []syscall.Signal{fuzzIgnored},
		ReloadSignal:  fuzzReload,
		OnReload: func() error {
			run.locked(func() { run.reloads++ })
			run.hook(hookReload)

			return nil
		},
		RestartDelay: run.cfg.delay,
		CheckPeriod:  checkPeriodOf(run.cfg),
		Check:        checkOf(run),
		Cleanup:      run.cleanup,
		OnEvent:      run.onEvent,
		Start:        nil,
	})
	if err != nil {
		run.t.Fatal(err)
	}

	engine.subscribe = func() (<-chan os.Signal, func()) { return run.signals, func() {} }
	engine.spawn = run.spawn
	engine.clock = run.clock

	rival := run.startRival()

	go func() {
		defer close(run.finished)
		defer func() { run.panicValue = recover() }()

		run.result = engine.Run(run.ctx)
	}()

	run.settle()

	batch := false

	for index := 0; index < len(run.steps) && !run.isFinished(); index++ {
		step := run.steps[index]

		switch step.op {
		case opInline:
			if index+1 < len(run.steps) {
				kind, inner := fuzzHook(step.arg%byte(hookCount)), run.steps[index+1]

				run.locked(func() { run.inline[kind] = append(run.inline[kind], inner) })

				index++
			}

			continue
		case opPanic:
			run.locked(func() { run.panicAt[step.arg%byte(hookCount)] = true })

			continue
		case opBatch:
			batch = true

			continue
		default:
			run.apply(step)
		}

		if batch {
			batch = false

			continue
		}

		run.settle()
		run.checkSettled()
	}

	run.settle()
	run.finish()
	rival()
	run.checkEnd()
}

func childPidFileOf(run *fuzzRun) string {
	if run.cfg.childPidFile {
		return run.childPid
	}

	return ""
}

func checkPeriodOf(cfg fuzzConfig) time.Duration {
	if cfg.checks {
		return fuzzCheckPeriod
	}

	return 0
}

func checkOf(run *fuzzRun) func(ctx context.Context) error {
	if run.cfg.checks {
		return run.check
	}

	return nil
}

// startRival races the engine for a stale supervisor pid file, if the script
// asks for it, and returns the function that gives the file up.
func (run *fuzzRun) startRival() func() {
	if !run.cfg.rival {
		return func() {}
	}

	err := os.WriteFile(run.pidFile, []byte(strconv.Itoa(fuzzStalePid)), 0o600)
	if err != nil {
		run.t.Fatal(err)
	}

	result := make(chan *pidFile, 1)

	go func() {
		owned, _ := acquirePidFile(run.pidFile, os.Getppid())
		result <- owned
	}()

	return func() {
		owned := <-result
		if owned == nil {
			return
		}

		run.locked(func() { run.rivalOwned = true })

		_ = owned.release()
	}
}

// finish ends a run the script left going: a stop, time for the stop
// timeout, and a cancelled context if that is not enough.
func (run *fuzzRun) finish() {
	if run.isFinished() {
		return
	}

	run.inject(syscall.SIGTERM)
	run.settle()

	for range 3 {
		if run.isFinished() {
			return
		}

		run.clock.advance(fuzzStopTimeout + fuzzUnit)
		run.settle()
	}

	if !run.isFinished() {
		run.failf("Run went on after a stop and the stop timeout")
		run.cancel()
		run.clock.advance(fuzzStopTimeout + fuzzUnit)
		run.settle()
	}

	select {
	case <-run.finished:
	case <-time.After(fuzzSettleTimeout):
		run.t.Fatalf("Run does not return\nscript %s\n%s", run.describe(),
			strings.Join(engineStacks(), "\n\n"))
	}
}

// checkEnd checks the state Run leaves behind.
func (run *fuzzRun) checkEnd() {
	// The engine's goroutines end with Run.
	deadline := time.Now().Add(fuzzSettleTimeout)
	for len(engineStacks()) > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Microsecond)
	}

	if stacks := engineStacks(); len(stacks) > 0 {
		run.failf("goroutines outlived Run:\n%s", strings.Join(stacks, "\n\n"))
	}

	run.mu.Lock()
	defer run.mu.Unlock()

	for _, proc := range run.procs {
		if !proc.reaped() {
			run.failf("Run returned while %d was not reaped", proc.pid)
		}
	}

	if !run.ownedByEngine {
		// A rival owned the pid file first: nothing ran, nothing was
		// cleaned. A rival that owned it after Run is no conflict.
		if !run.rivalOwned {
			run.failf("the engine never owned its pid file, and no rival did")
		}

		run.checkResult(OpPidFile)

		return
	}

	if run.cleanups != 1 {
		run.failf("Cleanup ran %d times", run.cleanups)
	}

	for _, path := range []string{run.pidFile, run.childPid} {
		if _, ok := run.pidFileHolds(path); ok {
			run.failf("%s outlived Run", path)
		}
	}

	if run.panicValue != nil {
		if run.panicValue != errFuzzPanic { //nolint:errorlint // The very value panicked with.
			run.failf("Run panicked with %v", run.panicValue)
		}

		return
	}

	if run.running != 0 {
		run.failf("Run returned without reporting the exit of %d", run.running)
	}

	// Every signal sent went with an event, bar the stop of a cancelled
	// context, which the Exit took.
	if len(run.sends) > 0 || run.reloads > 0 {
		run.failf("Run returned after sending %v and reloading %d times with no event",
			run.sends, run.reloads)
	}

	run.checkResult(0)
}

// checkResult checks the error Run returned against what the script caused.
func (run *fuzzRun) checkResult(forced Op) {
	var runErr *Error

	isError := errors.As(run.result, &runErr)

	switch {
	case forced != 0:
		if !isError || runErr.Op != forced {
			run.failf("Run returned %v, want %s", run.result, forced)
		}
	case run.checkFailed:
		if !isError || runErr.Op != OpCheck || !errors.Is(run.result, errFuzzCheck) {
			run.failf("Run returned %v after a failed check", run.result)
		}
	case run.result == nil:
		if !run.stopRequested && !run.restartSaidNo {
			run.failf("Run returned nil without a stop or a declined restart")
		}
	case !isError:
		run.failf("Run returned %v", run.result)
	default:
		causes := map[Op]error{
			OpNext:    errFuzzNext,
			OpStart:   errFuzzStart,
			OpRestart: errFuzzRestart,
		}

		cause, known := causes[runErr.Op]
		if !known || !run.used[cause] || !errors.Is(run.result, cause) {
			run.failf("Run returned %v, which the script did not cause", run.result)
		}
	}
}

// replayScript runs a script fuzzReplays times, failing t on a violation.
func replayScript(t *testing.T, data []byte) {
	t.Helper()

	cfg, steps := decodeScript(data)

	for replay := range fuzzReplays {
		dir := t.TempDir()
		ctx, cancel := context.WithCancel(context.Background())
		run := &fuzzRun{
			t:           t,
			script:      data,
			cfg:         cfg,
			steps:       steps,
			pidFile:     filepath.Join(dir, "supervisor.pid"),
			childPid:    filepath.Join(dir, "child.pid"),
			clock:       &fakeClock{now: 0, timers: nil},
			signals:     make(chan os.Signal, 4*fuzzMaxSteps),
			ctx:         ctx,
			cancel:      cancel,
			finished:    make(chan struct{}),
			completions: make(chan error, 1),
			failed:      map[int]bool{},
			reported:    map[int]bool{},
			used:        map[error]bool{},
		}

		run.drive()
		cancel()

		if len(run.violations) > 0 {
			t.Fatalf("replay %d of script %x\n%s\nviolations:\n  %s", replay, data,
				run.describe(), strings.Join(run.violations, "\n  "))
		}
	}
}
