package binary

import (
	"context"
	"os"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/sdk/v3/log"
)

const (
	// commandStopTimeout is how long an interrupt waits for the commands it
	// stopped - a make and the compilers it runs, a git clone - to exit
	// before it removes the directories they write into.
	commandStopTimeout = 10 * time.Second
	// commandWaitDelay is how long a command stopped with SIGTERM has before
	// it is killed.
	commandWaitDelay = 5 * time.Second
	// signalExitBase is what a shell adds to the number of the signal that
	// ended a process to make its exit status.
	signalExitBase = 128
)

// interruptState is the state of WatchInterrupts: the temporary paths to
// remove on SIGINT or SIGTERM and the commands to stop first.
type interruptState struct {
	mu sync.Mutex
	// paths are the temporary files and directories to remove, in the
	// order they were registered.
	paths []*string
	// keep keeps the paths, naming them, as --no-clean asks.
	keep bool
	// interrupted tells that an interrupt came: a command started after it
	// is stopped at once.
	interrupted bool
	// stops stop the commands ExecuteCommand runs.
	stops []*context.CancelFunc
	// commands counts the commands ExecuteCommand runs.
	commands sync.WaitGroup
	// ended is closed once the watch is over: an interrupt has cleaned up
	// and ended tt, which only a stub of exitOnInterrupt returns from, or
	// the watch was stopped. It is nil with no watch.
	ended chan struct{}
}

var interrupts = &interruptState{
	mu:          sync.Mutex{},
	paths:       nil,
	keep:        false,
	interrupted: false,
	stops:       nil,
	commands:    sync.WaitGroup{},
	ended:       nil,
}

// exitOnInterrupt ends tt after an interrupt has cleaned up, as the signal
// would have: the default action of the signal, or exit status 128+signal
// if it does not end the process. A variable for the tests.
var exitOnInterrupt = func(sig os.Signal) {
	number, ok := sig.(syscall.Signal)
	if !ok {
		os.Exit(1)
	}

	signal.Reset(sig)

	_ = syscall.Kill(os.Getpid(), number)

	time.Sleep(time.Second)
	os.Exit(signalExitBase + int(number))
}

// WatchInterrupts makes SIGINT and SIGTERM, until the returned function is
// called, stop the commands ExecuteCommand runs, remove the paths
// RemoveOnInterrupt registered - or name them with keep, as --no-clean
// asks - and end tt. An interrupted source build leaves no clone or build
// directory behind: the deferred cleanups of the installation do not run
// when the signal ends tt. The command the interrupt stops does not return
// to the installation (commandContext), whose cleanups would remove, keep
// or name the files at the same time as the interrupt.
func WatchInterrupts(keep bool) func() {
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})

	// finished is closed once an interrupt has cleaned up and exited, which
	// only a stub of exitOnInterrupt returns from, or once the watch is
	// stopped with no interrupt.
	finished := make(chan struct{})

	interrupts.mu.Lock()

	interrupts.keep = keep
	interrupts.interrupted = false
	interrupts.ended = finished

	interrupts.mu.Unlock()

	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		defer close(finished)

		select {
		case sig := <-signals:
			interrupts.cleanUp(sig)
			exitOnInterrupt(sig)
		case <-done:
		}
	}()

	return func() {
		signal.Stop(signals)
		close(done)

		interrupts.mu.Lock()

		interrupted := interrupts.interrupted

		interrupts.mu.Unlock()

		// An installation that returns here after an interrupt - with no
		// command running, it failed on the files the interrupt removed, or
		// it was done - waits as a stopped command does: tt is to end by
		// the signal once the files are removed, not with the error of the
		// installation before that.
		if interrupted {
			<-finished
		}

		interrupts.mu.Lock()
		defer interrupts.mu.Unlock()

		interrupts.paths = nil
		interrupts.interrupted = false
		interrupts.ended = nil
	}
}

// cleanUp stops the running commands and removes the registered paths,
// newest first.
func (state *interruptState) cleanUp(sig os.Signal) {
	name := map[os.Signal]string{syscall.SIGINT: "SIGINT", syscall.SIGTERM: "SIGTERM"}[sig]
	log.Warnf("Interrupted by %s: stopping and removing temporary files...", name)

	state.mu.Lock()

	state.interrupted = true
	for _, stop := range state.stops {
		(*stop)()
	}

	keep := state.keep
	state.mu.Unlock()

	stopped := make(chan struct{})

	go func() {
		state.commands.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(commandStopTimeout):
		log.Warnf("Commands still run after %s; removing the files anyway", commandStopTimeout)
	}

	state.mu.Lock()

	paths := slices.Clone(state.paths)

	state.mu.Unlock()

	for _, path := range slices.Backward(paths) {
		if keep {
			log.Infof("Temporary files kept at: %s", *path)

			continue
		}

		err := os.RemoveAll(*path)
		if err != nil {
			log.Warnf("Failed to remove %s: %v", *path, err)
		}
	}
}

// RemoveOnInterrupt registers path, a temporary file or directory, to be
// removed when WatchInterrupts sees SIGINT or SIGTERM. The returned
// function unregisters it, once the caller has removed it or handed it
// over; the end of the watch forgets every path.
func RemoveOnInterrupt(path string) func() {
	entry := &path

	interrupts.mu.Lock()
	defer interrupts.mu.Unlock()

	interrupts.paths = append(interrupts.paths, entry)

	return func() {
		interrupts.mu.Lock()
		defer interrupts.mu.Unlock()

		interrupts.paths = slices.DeleteFunc(interrupts.paths, func(registered *string) bool {
			return registered == entry
		})
	}
}

// commandContext returns the context ExecuteCommand runs a command with,
// which an interrupt cancels, and registers the command until done is
// called. After an interrupt, done returns only once the watch is over,
// which in tt it never is: the interrupt ends tt. Otherwise the caller of
// the command the interrupt stopped would get the error and unwind, and
// the deferred cleanups of the installation would remove its files, or
// with --no-clean name them, and forget them while the interrupt removes
// or names the same files: a directory forgotten first was neither removed
// nor named.
func commandContext() (context.Context, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	entry := &cancel

	interrupts.mu.Lock()
	defer interrupts.mu.Unlock()

	if interrupts.interrupted {
		cancel()
	}

	interrupts.stops = append(interrupts.stops, entry)
	interrupts.commands.Add(1)

	done := func() {
		interrupts.mu.Lock()

		interrupts.stops = slices.DeleteFunc(interrupts.stops,
			func(registered *context.CancelFunc) bool { return registered == entry })

		interrupts.mu.Unlock()

		cancel()
		interrupts.commands.Done()

		// Read after Done: an interrupt that came while the command ran has
		// set interrupted by now, and one that comes later finds no command
		// to wait for.
		interrupts.mu.Lock()

		interrupted, ended := interrupts.interrupted, interrupts.ended

		interrupts.mu.Unlock()

		if interrupted && ended != nil {
			<-ended
		}
	}

	return ctx, done
}
