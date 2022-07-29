package running

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/ttlog"
)

// InstanceBuilder interface is used for creating instances.
type InstanceBuilder interface {
	createInstance() (*Instance, error)
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
	// restartable is the flag is set to "true" if the Instance
	// should be restarted in case of termination.
	restartable bool
	// restartTimeout describes the timeout between
	// restarting the Instance.
	restartTimeout time.Duration
	// done channel used to inform the signal handle goroutine
	// about termination of the Instance.
	done chan bool
	// instanceBuilder is an interface used to build instances.
	instanceBuilder InstanceBuilder
	// configPath is used to re-read restartable option.
	configPath string
}

// newWatchdog creates a new instance of Watchdog.
func newWatchdog(restartable bool,
	restartTimeout time.Duration, logger *ttlog.Logger,
	instanceBuilder InstanceBuilder, configPath string) *Watchdog {
	wd := Watchdog{Instance: nil, logger: logger, restartable: restartable,
		restartTimeout: restartTimeout, instanceBuilder: instanceBuilder, configPath: configPath}

	wd.done = make(chan bool, 1)

	return &wd
}

// Start starts the Instance and signal handling.
func (wd *Watchdog) Start() {
	// The Instance must be restarted on completion if the "restartable"
	// parameter is set to "true".
	for {
		var err error
		// Create Instance.
		if wd.Instance, err = wd.instanceBuilder.createInstance(); err != nil {
			wd.logger.Printf(`Watchdog(ERROR): "%v".`, err)
			break
		}
		wd.logger = wd.Instance.logger
		// Start the Instance and forwarding signals (except  SIGINT and SIGTERM)
		if err := wd.Instance.Start(); err != nil {
			wd.logger.Printf(`Watchdog(ERROR): "%v".`, err)
			break
		}
		wd.startSignalHandling()

		// Wait while the Instance will be terminated.
		if err := wd.Instance.Wait(); err != nil {
			wd.logger.Printf(`Watchdog(WARN): "%v".`, err)
		}

		// Set Instance process completion indication.
		wd.done <- true
		// Wait for the signal processing goroutine to complete.
		wd.doneBarrier.Wait()

		// Parse cfg again to check if values changed.
		if wd.configPath != "" {
			newCliopts, _ := configure.GetCliOpts(wd.configPath)
			wd.restartable = newCliopts.App.Restartable
		}

		// Stop the process if the Instance is not restartable.
		if !wd.restartable {
			wd.logger.Println("Watchdog(INFO): the Instance has shutdown.")
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
