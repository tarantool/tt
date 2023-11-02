package running

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/cli/ttlog"
)

// Provider interface provides Watchdog methods to get objects whose creation
// and updating may depend on changing external parameters (such as configuration
// file).
type Provider interface {
	// CreateInstance is used to create a new instance on restart.
	CreateInstance(logger *ttlog.Logger) (Instance, error)
	// UpdateLogger updates the logger settings or creates a new logger,
	// if passed nil.
	UpdateLogger(logger *ttlog.Logger) (*ttlog.Logger, error)
	// IsRestartable checks
	IsRestartable() (bool, error)
}

// Watchdog is a process that controls an Instance process.
type Watchdog struct {
	// instance describes the controlled Instance.
	instance Instance
	// logger represents an active logging object.
	logger *ttlog.Logger
	// doneBarrier used to indicate the completion of the
	// signal handling goroutine.
	doneBarrier sync.WaitGroup
	// restartTimeout describes the timeout between
	// restarting the Instance.
	restartTimeout time.Duration
	// done channel used to inform the signal handle goroutine
	// about termination of the Instance.
	done chan bool
	// provider provides Watchdog methods to get objects whose creation
	// and updating may depend on changing external parameters
	// (such as configuration file).
	provider Provider
	// stopMutex used to avoid a race condition under shouldStop field.
	stopMutex sync.Mutex
	// shouldStop indicates whether the Watchdog should be stopped.
	shouldStop bool
	// preStartAction is a hook that is to be run before the start of a new Instance.
	preStartAction func() error
}

// NewWatchdog creates a new instance of Watchdog.
func NewWatchdog(restartable bool, restartTimeout time.Duration, logger *ttlog.Logger,
	provider Provider, preStartAction func() error) *Watchdog {
	wd := Watchdog{instance: nil, logger: logger, restartTimeout: restartTimeout,
		provider: provider, preStartAction: preStartAction}

	wd.done = make(chan bool, 1)

	return &wd
}

// Start starts the Instance and signal handling.
func (wd *Watchdog) Start() error {
	var err error
	// Create Instance.
	if wd.instance, err = wd.provider.CreateInstance(wd.logger); err != nil {
		wd.logger.Printf(`(ERROR): instance creation failed: %v.`, err)
		return err
	}

	// The signal handling loop must be started before the instance
	// get started for avoiding a race condition between tt start
	// and tt stop. This way we avoid a situation when we receive
	// a signal before starting a handler for it.
	wd.startSignalHandling()

	if err = wd.preStartAction(); err != nil {
		wd.logger.Printf(`(ERROR): Pre-start action error: %v`, err)
		// Finish the signal handling goroutine.
		wd.done <- true
		return err
	}

	// The Instance must be restarted on completion if the "restartable"
	// parameter is set to "true".
	for {
		var err error

		wd.stopMutex.Lock()
		if wd.shouldStop {
			wd.logger.Printf(`(ERROR): terminated before instance start.`)
			wd.stopMutex.Unlock()
			return nil
		}
		// Start the Instance.
		if err := wd.instance.Start(); err != nil {
			wd.logger.Printf(`(ERROR):  instance start failed: %v.`, err)
			wd.stopMutex.Unlock()
			break
		}
		wd.stopMutex.Unlock()

		// Wait while the Instance will be terminated.
		if err := wd.instance.Wait(); err != nil {
			wd.logger.Printf(`(WARN): "%v".`, err)
		}

		// Set Instance process completion indication.
		wd.done <- true
		// Wait for the signal processing goroutine to complete.
		wd.doneBarrier.Wait()

		// Stop the process if the Instance is not restartable.
		restartable, err := wd.provider.IsRestartable()
		if err != nil {
			wd.logger.Println("(ERROR): can't check if the instance is restartable.")
			break
		}
		if wd.shouldStop || !restartable {
			wd.logger.Println("(INFO): the Instance has shutdown.")
			break
		}

		if logger, err := wd.provider.UpdateLogger(wd.logger); err != nil {
			wd.logger.Println("(ERROR): can't update logger parameters.")
			break
		} else {
			wd.logger = logger
		}
		wd.logger.Printf(`(INFO): waiting for restart timeout %s.`, wd.restartTimeout)
		time.Sleep(wd.restartTimeout)

		wd.shouldStop = false

		// Recreate Instance.
		if wd.instance, err = wd.provider.CreateInstance(wd.logger); err != nil {
			wd.logger.Printf(`(ERROR): "%v".`, err)
			return err
		}

		// Before the restart of an instance start a new signal handling loop.
		wd.startSignalHandling()
	}
	return nil
}

// startSignalHandling starts signal handling in a separate goroutine.
func (wd *Watchdog) startSignalHandling() {
	sigChan := make(chan os.Signal, 1)
	// Reset the signal mask before starting of the new loop.
	signal.Reset()
	signal.Notify(sigChan)

	// This call is needed to ignore SIGURG signals which are part of
	// preemptive multitasking implementation in go. See:
	// https://go.googlesource.com/proposal/+/master/design/24543-non-cooperative-preemption.md.
	// Also, there is no way to detect if a signal is related to the runtime or not:
	// https://github.com/golang/go/issues/37942.
	signal.Ignore(syscall.SIGURG)

	// Set barrier to synchronize with the main loop when the Instance stops.
	wd.doneBarrier.Add(1)

	// Start signals handling.
	go func() {
		// Set indication that the signal handling has been completed.
		defer wd.doneBarrier.Done()

		for {
			select {
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGINT, syscall.SIGTERM:
					wd.stopMutex.Lock()
					// If we receive one of the "stop" signals, the
					// program should be terminated.
					wd.shouldStop = true
					wd.stopMutex.Unlock()
					if wd.instance.IsAlive() {
						wd.instance.Stop(30 * time.Second)
					}
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
			case _ = <-wd.done:
				return
			}
		}
	}()
}
