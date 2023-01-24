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
	CreateInstance(logger *ttlog.Logger) (*Instance, error)
	// UpdateLogger updates the logger settings or creates a new logger,
	// if passed nil.
	UpdateLogger(logger *ttlog.Logger) (*ttlog.Logger, error)
	// IsRestartable checks
	IsRestartable() (bool, error)
}

// Watchdog is a process that controls an Instance process.
type Watchdog struct {
	// Instance describes the controlled Instance.
	Instance *Instance
	// logger represents an active logging object.
	logger *ttlog.Logger
	// doneBarrier used to indicate the completion of the
	// signal handling goroutine.
	doneBarrier sync.WaitGroup
	// restartTimeout describes the timeout between
	// restarting the Instance.
	restartTimeout time.Duration
	// stopped indicates whether Watchdog was stopped.
	stopped bool
	// stopMutex used to avoid a race condition under stopped field.
	stopMutex sync.Mutex
	// done channel used to inform the signal handle goroutine
	// about termination of the Instance.
	done chan bool
	// provider provides Watchdog methods to get objects whose creation
	// and updating may depend on changing external parameters
	// (such as configuration file).
	provider Provider
}

// NewWatchdog creates a new instance of Watchdog.
func NewWatchdog(restartable bool, restartTimeout time.Duration, logger *ttlog.Logger,
	provider Provider) *Watchdog {
	wd := Watchdog{Instance: nil, logger: logger, restartTimeout: restartTimeout,
		provider: provider}

	wd.done = make(chan bool, 1)
	wd.stopped = false

	return &wd
}

// Start starts the Instance and signal handling.
func (wd *Watchdog) Start() {
	// The Instance must be restarted on completion if the "restartable"
	// parameter is set to "true".
	for {
		var err error
		// Create Instance.
		if wd.Instance, err = wd.provider.CreateInstance(wd.logger); err != nil {
			wd.logger.Printf(`Watchdog(ERROR): "%v".`, err)
			break
		}
		wd.logger = wd.Instance.logger
		// Start the Instance and forwarding signals (except  SIGINT and SIGTERM)
		wd.startSignalHandling()
		wd.stopMutex.Lock()
		if !wd.stopped {
			if err := wd.Instance.Start(); err != nil {
				wd.logger.Printf(`Watchdog(ERROR): "%v".`, err)
				wd.stopMutex.Unlock()
				break
			}
		}
		wd.stopMutex.Unlock()

		// Wait while the Instance will be terminated.
		if err := wd.Instance.Wait(); err != nil {
			wd.logger.Printf(`Watchdog(WARN): "%v".`, err)
		}

		// Set Instance process completion indication.
		wd.done <- true
		// Wait for the signal processing goroutine to complete.
		wd.doneBarrier.Wait()

		// Stop the process if the Instance is not restartable.
		restartable, err := wd.provider.IsRestartable()
		if err != nil {
			wd.logger.Println("Watchdog(ERROR): can't check if the instance is restartable.")
			break
		}
		if wd.stopped || !restartable {
			wd.logger.Println("Watchdog(INFO): the Instance has shutdown.")
			break
		}

		if logger, err := wd.provider.UpdateLogger(wd.logger); err != nil {
			wd.logger.Println("Watchdog(ERROR): can't update logger parameters.")
			break
		} else {
			wd.logger = logger
		}
		time.Sleep(wd.restartTimeout)
	}
}

// startSignalHandling starts signal handling in a separate goroutine.
func (wd *Watchdog) startSignalHandling() {
	sigChan := make(chan os.Signal, 1)
	// Reset unregisters all previous handlers for interrupt signals.
	signal.Reset(syscall.SIGINT,
		syscall.SIGTERM, syscall.SIGHUP)
	signal.Notify(sigChan)

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
					wd.Instance.Stop(30 * time.Second)
					// If we receive one of the "stop" signals, the
					// program should be terminated.
					wd.stopped = true
					wd.stopMutex.Unlock()
				case syscall.SIGHUP:
					// Rotate the log files.
					wd.logger.Rotate()
				default:
					wd.Instance.SendSignal(sig)
				}
			case _ = <-wd.done:
				signal.Reset()
				return
			}
		}
	}()
}
