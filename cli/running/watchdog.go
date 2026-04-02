package running

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/cli/ttlog"
	"github.com/tarantool/tt/lib/integrity"
)

// Provider interface provides Watchdog methods to get objects whose creation
// and updating may depend on changing external parameters (such as configuration
// file).
type Provider interface {
	// CreateInstance is used to create a new instance on restart.
	CreateInstance(logger ttlog.Logger) (Instance, error)
	// UpdateLogger updates the logger settings or creates a new logger,
	// if passed nil.
	UpdateLogger(logger ttlog.Logger) (ttlog.Logger, error)
	// IsRestartable checks
	IsRestartable() (bool, error)
}

// Watchdog is a process that controls an Instance process.
type Watchdog struct {
	// instance describes the controlled Instance.
	instance Instance
	// logger represents an active logging object.
	logger ttlog.Logger
	// doneBarrier used to indicate the completion of the
	// signal handling and integrity checking goroutines.
	doneBarrier sync.WaitGroup
	// restartTimeout describes the timeout between
	// restarting the Instance.
	restartTimeout time.Duration
	// provider provides Watchdog methods to get objects whose creation
	// and updating may depend on changing external parameters
	// (such as configuration file).
	provider Provider
	// preStartAction is a hook that is to be run before the start of a new Instance.
	preStartAction func() error
	// IntegrityCtx contains information necessary to perform integrity checks.
	integrityCtx integrity.IntegrityCtx
	// integrityCheckPeriod is period between integrity checks.
	integrityCheckPeriod time.Duration
	// instShutdownCh signals instance termination to the event loop.
	instTerminatedCh chan struct{}
	// osSignalCh receives OS signals for the whole watchdog lifetime.
	osSignalCh chan os.Signal
}

const (
	signalBufferSize = 128
)

// NewWatchdog creates a new instance of Watchdog.
func NewWatchdog(restartable bool, restartTimeout time.Duration, logger ttlog.Logger,
	provider Provider, preStartAction func() error,
	integrityCtx integrity.IntegrityCtx, integrityCheckPeriod time.Duration,
) *Watchdog {
	wd := Watchdog{
		instance:             nil,
		logger:               logger,
		restartTimeout:       restartTimeout,
		provider:             provider,
		preStartAction:       preStartAction,
		integrityCtx:         integrityCtx,
		integrityCheckPeriod: integrityCheckPeriod,
		instTerminatedCh:     make(chan struct{}),
		osSignalCh:           make(chan os.Signal, 1),
	}

	return &wd
}

func (wd *Watchdog) Start() {
	wd.eventLoop()
}

// eventLoop serializes instance lifecycle: start, signal handling, stop, and restart.
func (wd *Watchdog) eventLoop() {
	wd.startSignalHandling()
	defer signal.Stop(wd.osSignalCh)

	if err := wd.preStartAction(); err != nil {
		wd.logger.Printf(`(ERROR): Pre-start action error: %v`, err)
		return
	}

outer:
	for {
		// Ignore signals accumulated while the previous instance was down.
		wd.dropPendingSignals()

		handlerCtx, handlerCancel := context.WithCancel(context.Background())

		// Make local signal channel for every cycle, so that
		// the old signals in the buffer cannot affect the already new cycle.
		signalCh := make(chan os.Signal, signalBufferSize)

		// Buffer signals during startup, to process them after.
		wd.signalHandler(handlerCtx, signalCh)

		// cleanup stops signal handler and integration check.
		cleanup := func() {
			handlerCancel()
			wd.doneBarrier.Wait()
		}

		// Every iteration we start the instance and then
		// check events.
		if started := wd.startInstance(handlerCtx); !started {
			// We have already logged the error.
			cleanup()
			return
		}

		shouldStop := false

		for {
			// We have two cases during instance's lifecycle:
			// 1. The user has sent a `tt stop` or another signal.
			// 2. The instance has terminated, and we should(n't) restart it.
			// In the second case, we have no to stop instance.
			select {
			case sig := <-signalCh:
				// After handling a stop signal, eventually we will get an
				// event in instTerminatedCh.
				shouldStop = wd.sendSignal(sig) || shouldStop
			case <-wd.instTerminatedCh:
				cleanup()

				if !wd.shouldRestart() || shouldStop {
					wd.logger.Println("(INFO): the Instance has shutdown.")
					return
				}

				if logger, err := wd.provider.UpdateLogger(wd.logger); err != nil {
					wd.logger.Println("(ERROR): can't update logger parameters.")
					return
				} else {
					wd.logger = logger
				}

				wd.logger.Printf(`(INFO): waiting for restart timeout %s.`, wd.restartTimeout)
				time.Sleep(wd.restartTimeout)

				// Start a new start instance loop.
				continue outer
			}
		}
	}
}

// shouldRestart checks if the instance should be restarted.
func (wd *Watchdog) shouldRestart() bool {
	restartable, err := wd.provider.IsRestartable()
	if err != nil {
		wd.logger.Println("(ERROR): can't check if the instance is restartable.")
		return false
	}

	return restartable
}

// startInstance starts the Instance and signal handling.
func (wd *Watchdog) startInstance(ctx context.Context) bool {
	var err error

	// Create Instance.
	if wd.instance, err = wd.provider.CreateInstance(wd.logger); err != nil {
		wd.logger.Printf(`(ERROR): instance creation failed: %v.`, err)
		return false
	}

	// Launch integrity checking goroutine.
	if wd.integrityCheckPeriod != 0 {
		wd.logger.Printf("(INFO): starting periodic integrity checks each %s.",
			wd.integrityCheckPeriod)
		wd.startIntegrityChecks(ctx)
	}

	// Start the Instance.
	if err := wd.instance.Start(context.Background()); err != nil {
		wd.logger.Printf(`(ERROR):  instance start failed: %v.`, err)
		return false
	}

	// Wait for the instance to terminate.
	go func() {
		if err := wd.instance.Wait(); err != nil {
			wd.logger.Printf(`(WARN): "%v".`, err)
		}

		wd.instTerminatedCh <- struct{}{}
	}()

	return true
}

// sendSignal sends signal to the instance and
// returns whether watchdog should stop.
func (wd *Watchdog) sendSignal(sig os.Signal) bool {
	wd.logger.Printf("(INFO): %s received.", sig.String())

	switch sig {
	case syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
		if wd.instance.IsAlive() {
			wd.instance.StopWithSignal(30*time.Second, sig)
		}
		return true
	case syscall.SIGHUP:
		// Rotate the log files.
		wd.logger.Rotate()
		if wd.instance.IsAlive() {
			wd.instance.SendSignal(sig)
		}
	default:
		if wd.instance.IsAlive() {
			wd.instance.SendSignal(sig)
		}
	}

	return false
}

// startIntegrityChecks launches goroutine that performs periodic integrity checks.
func (wd *Watchdog) startIntegrityChecks(ctx context.Context) {
	ticker := time.NewTicker(wd.integrityCheckPeriod)

	wd.doneBarrier.Go(func() {
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				wd.logger.Printf("(INFO): periodic integrity check successfully passed.")

				err := wd.integrityCtx.Repository.ValidateAll()
				if err != nil {
					// Integrity check failed.
					wd.logger.Printf("(ERROR): periodic integrity check failed: %q.", err)
					wd.instance.SendSignal(syscall.SIGKILL)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	})
}

// startSignalHandling starts signal handling in a separate goroutine.
func (wd *Watchdog) startSignalHandling() {
	// Reset the signal mask before starting of the new loop.
	signal.Reset()
	signal.Notify(wd.osSignalCh)

	// This call is needed to ignore SIGURG signals which are part of
	// preemptive multitasking implementation in go. See:
	// https://go.googlesource.com/proposal/+/master/design/24543-non-cooperative-preemption.md.
	// Also, there is no way to detect if a signal is related to the runtime or not:
	// https://github.com/golang/go/issues/37942.
	signal.Ignore(syscall.SIGURG)
}

func (wd *Watchdog) dropPendingSignals() {
	select {
	case <-wd.osSignalCh:
	default:
	}
}

// signalHandler handles OS signals and sends them to the event loop.
func (wd *Watchdog) signalHandler(ctx context.Context, ch chan os.Signal) {
	wd.doneBarrier.Go(func() {
		for {
			select {
			case sig := <-wd.osSignalCh:
				select {
				// Send signal event to the eventloop.
				case ch <- sig:
				default:
					// If signals are sent to the instance without control,
					// when the buffer is filled, the signals will be dropped.
				}
			case <-ctx.Done():
				return
			}
		}
	})
}
