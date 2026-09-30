package supervisor

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The fuzz target drives the engine through a script decoded from the input:
// signals, exits, checks, time and the Source's answers, in the order the
// script gives them. The processes and the clock are fakes the script moves,
// so the only thing left to the scheduler is the order in which Go's select
// picks among channels that are ready together; each input is replayed a few
// times for that reason.
//
// Script layout: two header bytes, then two bytes per step (op, argument).
// Any byte string decodes, so the fuzzer never wastes an input. FUZZ_TRACE=1
// prints the events of a run to stderr, for reading a failing script.

const (
	// fuzzUnit is one unit of fake time.
	fuzzUnit        = time.Millisecond
	fuzzStopTimeout = 5 * fuzzUnit
	fuzzCheckPeriod = 3 * fuzzUnit
	// fuzzMaxSteps bounds the steps taken from one input.
	fuzzMaxSteps = 64
	// fuzzReplays is how many times one input runs.
	fuzzReplays = 3
	// fuzzSettleTimeout bounds the wait for the engine to settle; it is a
	// liveness check, not a pace.
	fuzzSettleTimeout = 10 * time.Second
	// fuzzFirstPid is the first pid of the fake children, far from real ones.
	fuzzFirstPid = 1 << 26
	// fuzzStalePid is a pid no process has.
	fuzzStalePid = 1<<31 - 2
)

// fuzzDelays are the restart delays a script picks from.
var fuzzDelays = [...]time.Duration{0, fuzzUnit, 4 * fuzzUnit, 20 * fuzzUnit}

// fuzzStops are the stop signals; a script picks one by its argument.
var fuzzStops = []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT}

const (
	fuzzReload  = syscall.SIGHUP
	fuzzForward = syscall.SIGUSR1
	fuzzIgnored = syscall.SIGUSR2
)

var (
	errFuzzCheck   = errors.New("fuzz: the check failed")
	errFuzzNext    = errors.New("fuzz: the Source has no Spec")
	errFuzzStart   = errors.New("fuzz: the child did not start")
	errFuzzRestart = errors.New("fuzz: the Source could not decide")
	errFuzzPanic   = errors.New("fuzz: a callback panicked")
)

// fuzzConfig is what the header bytes choose. The lowest bit of the first
// byte is spare.
type fuzzConfig struct {
	checks      bool
	forwardStop bool
	// stubborn children ignore the stop signals and die only of SIGKILL.
	stubborn bool
	// lateFails makes a check that is cancelled fail anyway, like a check
	// that found something and does not look at its context.
	lateFails    bool
	delay        time.Duration
	childPidFile bool
	// rival races the engine for a stale supervisor pid file.
	rival bool
}

type fuzzOp uint8

const (
	opStop        fuzzOp = iota // A stop signal; the argument picks which.
	opReload                    // The reload signal.
	opForward                   // A signal to forward.
	opIgnored                   // An ignored signal.
	opExit                      // The running child exits.
	opAdvance                   // Time advances by 1-16 units.
	opCheckPass                 // The check in progress, or the next one, passes.
	opCheckFail                 // The check in progress, or the next one, fails.
	opCancel                    // The context is cancelled.
	opFailNext                  // The next Source.Next fails.
	opFailStart                 // The next start fails.
	opRestartPlan               // The next Source.Restart says yes, no or fails.
	opInline                    // The next step happens inside a callback.
	opBatch                     // No settling after the next step.
	opPanic                     // A callback panics.
	opCount
)

// fuzzHook is a callback a step can be run inside, or can panic in.
type fuzzHook uint8

const (
	hookStarted fuzzHook = iota
	hookExit
	hookRestarting
	hookSignal
	hookChecked
	hookKilled
	hookNext
	hookRestart
	hookReload
	hookCount
)

type fuzzStep struct {
	op  fuzzOp
	arg uint8
}

func (step fuzzStep) String() string {
	names := [...]string{
		"stop", "reload", "forward", "ignored", "exit", "advance", "check-pass",
		"check-fail", "cancel", "fail-next", "fail-start", "restart-plan", "inline",
		"batch", "panic",
	}

	return fmt.Sprintf("%s(%d)", names[step.op], step.arg)
}

// decodeScript turns any byte string into a configuration and steps.
func decodeScript(data []byte) (fuzzConfig, []fuzzStep) {
	var header [2]byte

	copy(header[:], data)

	flags := header[0]
	cfg := fuzzConfig{
		checks:       flags&2 != 0,
		forwardStop:  flags&4 != 0,
		stubborn:     flags&8 != 0,
		lateFails:    flags&16 != 0,
		delay:        fuzzDelays[(flags>>5)&3],
		childPidFile: flags&128 != 0,
		rival:        header[1]&1 != 0,
	}

	var steps []fuzzStep

	if len(data) > len(header) {
		body := data[len(header):]
		for index := 0; index < len(body) && len(steps) < fuzzMaxSteps; index += 2 {
			step := fuzzStep{op: fuzzOp(body[index] % byte(opCount)), arg: 0}
			if index+1 < len(body) {
				step.arg = body[index+1]
			}

			steps = append(steps, step)
		}
	}

	return cfg, steps
}

// encodeScript is the inverse of decodeScript, for the seeds.
func encodeScript(flags, flags2 byte, steps ...fuzzStep) []byte {
	data := append(make([]byte, 0, 2+2*len(steps)), flags, flags2)

	for _, step := range steps {
		data = append(data, byte(step.op), step.arg)
	}

	return data
}

// Flags of the first header byte, for the seeds.
const (
	flagChecks       byte = 2
	flagForwardStop  byte = 4
	flagStubborn     byte = 8
	flagLateFails    byte = 16
	flagDelay1       byte = 1 << 5
	flagDelay20      byte = 3 << 5
	flagChildPidFile byte = 128
	flag2Rival       byte = 1
)

func step(op fuzzOp, arg uint8) fuzzStep { return fuzzStep{op: op, arg: arg} }

func inlineIn(kind fuzzHook, inner fuzzStep) []fuzzStep {
	return []fuzzStep{step(opInline, uint8(kind)), inner}
}

// fuzzSeeds are the scenarios where two events meet at a boundary of the
// engine, and a few ordinary lives.
func fuzzSeeds() map[string][]byte {
	seq := slices.Concat[[]fuzzStep]
	one := func(steps ...fuzzStep) []fuzzStep { return steps }

	return map[string][]byte{
		// A check fails as the child exits under it; the Source would
		// restart the child.
		"check-races-exit": encodeScript(flagChecks|flagLateFails|flagChildPidFile, 0,
			step(opAdvance, 2), step(opExit, 0)),
		// The same with a restart delay and a stop during it.
		"check-races-exit-then-stop": encodeScript(flagChecks|flagLateFails|flagDelay20, 0,
			step(opAdvance, 2), step(opExit, 0), step(opCancel, 0)),
		// A stop queued while the child exits, no restart delay: the stop and
		// the exit are ready together.
		"stop-queued-with-exit": encodeScript(flagChildPidFile, 0, seq(
			inlineIn(hookStarted, step(opStop, 1)), inlineIn(hookStarted, step(opExit, 0)),
			one(step(opExit, 0)))...),
		// A stop queued as the restart delay begins.
		"stop-queued-with-delay": encodeScript(0, 0, seq(
			inlineIn(hookRestarting, step(opStop, 0)), one(step(opExit, 0)))...),
		// A stop while the Source prepares the next Spec.
		"stop-during-next": encodeScript(0, 0, seq(
			inlineIn(hookNext, step(opStop, 1)), one(step(opExit, 0)))...),
		// A callback panics: at a start, on a signal, on a check.
		"panic-on-started": encodeScript(flagChildPidFile, 0,
			step(opPanic, uint8(hookStarted)), step(opExit, 0)),
		"panic-on-signal": encodeScript(flagChecks|flagChildPidFile, 0,
			step(opPanic, uint8(hookSignal)), step(opForward, 0)),
		"panic-on-checked": encodeScript(flagChecks|flagStubborn, 0,
			step(opPanic, uint8(hookChecked)), step(opAdvance, 2), step(opCheckPass, 0)),
		// Two starters race for a stale supervisor pid file.
		"rival-for-stale-pid-file": encodeScript(0, flag2Rival, step(opForward, 0)),
		// Ordinary lives: escalation of a stubborn child, a failed check that
		// kills a stubborn child, a failed check after a pass, Source
		// failures, ignored and reloaded signals.
		"stubborn-escalation": encodeScript(flagStubborn|flagChildPidFile, 0,
			step(opStop, 2), step(opAdvance, 4), step(opAdvance, 4)),
		"check-fails-kills-stubborn": encodeScript(flagChecks|flagStubborn, 0,
			step(opCheckFail, 0), step(opAdvance, 2)),
		"check-passes-then-fails": encodeScript(flagChecks|flagDelay1, 0,
			step(opCheckPass, 0), step(opAdvance, 2), step(opAdvance, 2),
			step(opCheckFail, 0), step(opAdvance, 2)),
		"source-failures": encodeScript(flagDelay1, 0,
			step(opRestartPlan, 0), step(opExit, 0), step(opAdvance, 1), step(opFailStart, 0),
			step(opExit, 0), step(opAdvance, 1)),
		"signals-idle-and-running": encodeScript(flagDelay20|flagForwardStop, 0,
			step(opIgnored, 0), step(opReload, 0), step(opForward, 0), step(opExit, 0),
			step(opForward, 0), step(opReload, 0), step(opIgnored, 0), step(opAdvance, 15),
			step(opAdvance, 5), step(opStop, 2)),
		"batched-cancel-and-exit": encodeScript(0, 0,
			step(opBatch, 0), step(opExit, 0), step(opCancel, 0)),
		"restart-declined": encodeScript(0, 0, step(opRestartPlan, 1), step(opExit, 0)),
	}
}

// FuzzEngine drives the engine through scripts and checks its invariants
// after every step.
//
// FUZZ_NO_SEEDS=1 starts from an empty script instead of the seeds, to
// measure how long the fuzzer takes to find a defect on its own.
func FuzzEngine(f *testing.F) {
	seeds := fuzzSeeds()
	if os.Getenv("FUZZ_NO_SEEDS") != "" {
		seeds = map[string][]byte{"empty": encodeScript(0, 0)}
	}

	for _, name := range slices.Sorted(maps.Keys(seeds)) {
		f.Add(seeds[name])
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		replayScript(t, data)
	})
}

// TestFuzzSeeds replays each seed of FuzzEngine by name, so that a failing
// one says which scenario broke.
func TestFuzzSeeds(t *testing.T) {
	seeds := fuzzSeeds()
	for _, name := range slices.Sorted(maps.Keys(seeds)) {
		t.Run(name, func(t *testing.T) { replayScript(t, seeds[name]) })
	}
}

// TestFuzzSeedsDecode pins that the seeds decode to the steps they were built
// from, so a seed tests the scenario its name says.
func TestFuzzSeedsDecode(t *testing.T) {
	data := encodeScript(flagChecks, flag2Rival, step(opInline, 3), step(opStop, 1))
	cfg, steps := decodeScript(data)

	assert.True(t, cfg.checks, "header decoded as %+v", cfg)
	assert.True(t, cfg.rival, "header decoded as %+v", cfg)
	assert.False(t, cfg.stubborn, "header decoded as %+v", cfg)
	assert.Equal(t, []fuzzStep{step(opInline, 3), step(opStop, 1)}, steps)

	_, empty := decodeScript(nil)
	assert.Empty(t, empty)
	assert.Equal(t, []byte{0, 0}, encodeScript(0, 0))
}
