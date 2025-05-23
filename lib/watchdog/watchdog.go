package watchdog

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/process_utils"
)

// Watchdog manages a child process, ensuring reliable startup, automatic restarts on failure,
// and graceful shutdown. It handles system signals, maintains PID file consistency,
// and provides thread-safe operations for concurrent process management.
type Watchdog struct {
	// cmd is the child process command (protected by cmdMutex).
	cmd *exec.Cmd
	// restartTimeout defines delay before restart (0 = immediate).
	restartTimeout time.Duration
	// shouldStop is atomic flag to prevent restarts when true.
	shouldStop atomic.Bool
	// doneBarrier waits for goroutines during shutdown.
	doneBarrier sync.WaitGroup
	// pidFile stores child process PID (protected by pidFileMutex).
	pidFile string
	// wdPidFile stores watchdog's own PID.
	wdPidFile string
	// cmdMutex guards cmd operations.
	cmdMutex sync.Mutex
	// pidFileMutex protects PID file access.
	pidFileMutex sync.Mutex
	// signalChan receives termination signals.
	signalChan chan os.Signal
	// processGroupPID stores Process Group ID for cleanup.
	processGroupPID atomic.Int32
	// startupComplete signals successful child process start.
	startupComplete chan struct{}
}

// NewWatchdog initializes a new Watchdog instance with the specified
// PID file paths and restart timeout duration. It sets up channels
// for signal notification and startup completion. Returns a pointer
// to the created Watchdog.
func NewWatchdog(pidFile, wdPidFile string, restartTimeout time.Duration) *Watchdog {
	return &Watchdog{
		pidFile:         pidFile,
		wdPidFile:       wdPidFile,
		restartTimeout:  restartTimeout,
		signalChan:      make(chan os.Signal, 1),
		startupComplete: make(chan struct{}),
	}
}

// Start begins monitoring and managing the target process.
// It handles process execution, restart logic, and signal processing.
func (wd *Watchdog) Start(bin string, args ...string) error {
	// Add to wait group to track active goroutines.
	wd.doneBarrier.Add(1)
	// Ensure we decrement wait group when done.
	defer wd.doneBarrier.Done()

	// Create context for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure context is canceled when we exit.

	// Register signal handler for termination signals.
	signal.Notify(wd.signalChan, syscall.SIGINT, syscall.SIGTERM)
	// Clean up signal handlers when done.
	defer signal.Stop(wd.signalChan)

	// Signal handling goroutine.
	go func() {
		select {
		case sig := <-wd.signalChan:
			// Only process signal if not already stopping.
			if !wd.shouldStop.Load() {
				log.Infof("Received signal: %v", sig)
				wd.Stop()
			}
		case <-ctx.Done():
		}
	}()

	// Main process management loop.
	for {
		// Check if we should stop before each iteration.
		if wd.shouldStop.Load() {
			return nil
		}

		// Start the managed process.
		wd.cmdMutex.Lock()
		wd.cmd = exec.Command(bin, args...)
		// Create new process group for proper signal handling.
		wd.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		// Start the process.
		if err := wd.cmd.Start(); err != nil {
			wd.cmdMutex.Unlock()
			log.Errorf("Failed to start process: %v", err)
			return err
		}

		// Store process group PID atomically
		wd.processGroupPID.Store(int32(wd.cmd.Process.Pid))
		wd.cmdMutex.Unlock()

		// Write PID files after successful start
		if err := wd.writePIDFiles(); err != nil {
			log.Errorf("Failed to write PID files: %v", err)
			_ = wd.terminateProcess() // Clean up if PID files fail.
			return err
		}

		log.Infof("Process started successfully")
		close(wd.startupComplete) // Signal that startup is complete.

		// Wait for process completion in separate goroutine.
		waitChan := make(chan error, 1)
		go func() { waitChan <- wd.cmd.Wait() }()

		select {
		case err := <-waitChan:
			// Check for stop signal after process exits.
			if wd.shouldStop.Load() {
				return nil
			}

			// Handle process exit status.
			if err != nil {
				if errors.As(err, new(*exec.ExitError)) {
					log.Warnf("Process exited with error: %v", err)
				} else {
					log.Errorf("Process failed: %v", err)
					return err
				}
			} else {
				log.Infof("Process completed successfully.")
			}

		case <-ctx.Done():
			// Context canceled - terminate process.
			_ = wd.terminateProcess()
			return nil
		}

		// Check stop condition again before restart.
		if wd.shouldStop.Load() {
			return nil
		}

		// Wait before restarting
		log.Infof("Waiting %s before restart...", wd.restartTimeout)
		select {
		case <-time.After(wd.restartTimeout):
			// Continue to next iteration after timeout.
		case <-ctx.Done():
			// Exit if context canceled during wait.
			return nil
		}

		// Reset startup complete channel for next iteration.
		wd.startupComplete = make(chan struct{})
	}
}

// Stop initiates a graceful shutdown of the Watchdog and its managed process.
// It ensures all resources are properly cleaned up and goroutines are terminated.
func (wd *Watchdog) Stop() {
	// Atomically set shouldStop flag to prevent multiple concurrent stops
	// CompareAndSwap ensures only one goroutine can execute the stop sequence
	if !wd.shouldStop.CompareAndSwap(false, true) {
		return // Already stopping or stopped.
	}

	// Ensure process startup is complete before attempting to stop.
	// This prevents races during process initialization.
	select {
	case <-wd.startupComplete:
		// Normal case - startup already completed.
	default:
		// Startup still in progress - wait for completion.
		log.Infof("Waiting for process startup...")
		<-wd.startupComplete
	}

	// Terminate the managed process.
	_ = wd.terminateProcess()

	// Clean up signal handling.
	signal.Stop(wd.signalChan)
	close(wd.signalChan)

	// Wait for all goroutines to complete.
	// This ensures we don't exit while signal handlers are still running.
	wd.doneBarrier.Wait()

	// Final log message indicating successful shutdown.
	log.Infof("Watchdog stopped.")
}

// terminateProcess sends a termination signal to the managed process.
func (wd *Watchdog) terminateProcess() error {
	wd.cmdMutex.Lock()
	defer wd.cmdMutex.Unlock()

	if wd.cmd == nil || wd.cmd.Process == nil {
		return nil
	}

	log.Infof("Stopping process...")

	pgid := int(wd.processGroupPID.Load())

	// Send SIGTERM to entire process group if available (preferred method).
	if pgid > 0 {
		return syscall.Kill(-pgid, syscall.SIGTERM)
	}

	return wd.cmd.Process.Signal(syscall.SIGTERM)
}

// writePIDFiles creates PID files for both the monitored process and the watchdog itself.
func (wd *Watchdog) writePIDFiles() error {
	wd.pidFileMutex.Lock()
	defer wd.pidFileMutex.Unlock()

	if wd.cmd == nil || wd.cmd.Process == nil {
		return errors.New("process is not running")
	}

	if err := process_utils.CreatePIDFile(wd.pidFile, wd.cmd.Process.Pid); err != nil {
		return err
	}

	log.Infof("Process PID %d written to %s", wd.cmd.Process.Pid, wd.pidFile)

	if isExistsAndRecord, _ := process_utils.ExistsAndRecord(wd.wdPidFile); !isExistsAndRecord {
		if err := process_utils.CreatePIDFile(wd.wdPidFile, os.Getpid()); err != nil {
			return err
		}
	}

	log.Infof("Watchdog PID %d written to %s", os.Getpid(), wd.wdPidFile)

	return nil
}
