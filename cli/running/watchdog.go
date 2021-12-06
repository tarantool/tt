package running

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// Watchdog is a process that controls an Instance process.
type Watchdog struct {
	// Instance describes the controlled Instance.
	Instance *Instance
	// doneBarrier used to indicate the completion of the
	// signal handling goroutine.
	doneBarrier sync.WaitGroup
	// restartable is the flag is set to "true" if the Instance
	// should be restarted in case of termination.
	restartable bool
	// restartTimeout describes the timeout between
	// restarting the Instance.
	restartTimeout time.Duration
	// done channel used to inform the signal handle goroutine
	// about termination of the Instance.
	done chan bool
}

// NewWatchdog creates a new instance of Watchdog.
func NewWatchdog(instance *Instance, restartable bool,
	restartTimeout time.Duration) *Watchdog {
	wd := Watchdog{Instance: instance, restartable: restartable,
		restartTimeout: restartTimeout}

	wd.done = make(chan bool, 1)
	return &wd
}

// Start starts the Instance and signal handling.
func (wd *Watchdog) Start() {
	// The Instance must be restarted on completion if the "restartable"
	// parameter is set to "true".
	for {
		// Start the Instance and forwarding signals (except  SIGINT and SIGTERM)
		if err := wd.Instance.Start(); err != nil {
			// TODO: log the error.
			break
		}
		wd.startSignalHandling()

		// Wait while the Instance will be terminated.
		if err := wd.Instance.Wait(); err != nil {
			// TODO: log the error.
		}

		// Set Instance process completion indication.
		wd.done <- true
		// Wait for the signal processing goroutine to complete.
		wd.doneBarrier.Wait()

		// Stop the process if the Instance is not restartable.
		if !wd.restartable {
			// TODO: Add event to log.
			break
		}

		time.Sleep(wd.restartTimeout)
	}
}

//startSignalHandling starts signal handling in a separate goroutine.
func (wd *Watchdog) startSignalHandling() {
	sigChan := make(chan os.Signal, 1)
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
					wd.Instance.Stop(30 * time.Second)
					// If we recive one of the "stop" signals, the
					// program should be terminated.
					wd.restartable = false
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
