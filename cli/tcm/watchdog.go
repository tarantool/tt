package tcm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Watchdog manages the lifecycle of a process.
type Watchdog struct {
	// The command to execute and monitor.
	cmd *exec.Cmd
	// Time to wait before restarting the process.
	restartTimeout time.Duration
	// Flag to indicate if the Watchdog should stop.
	shouldStop bool
	// Mutex to protect access to shouldStop.
	stopMutex sync.Mutex
	// WaitGroup to wait for all goroutines to finish.
	doneBarrier sync.WaitGroup
	// File to store the process PID.
	pidFile string
}

// NewWatchdog creates a new Watchdog instance.
func NewWatchdog(restartTimeout time.Duration) (*Watchdog, error) {
	return &Watchdog{
		restartTimeout: restartTimeout,
		pidFile:        "tcm/pidFile.pid",
	}, nil
}

// Start starts the process and monitors its execution.
func (wd *Watchdog) Start(bin string, args ...string) error {
	wd.doneBarrier.Add(1)
	defer wd.doneBarrier.Done()

	signalCtx, signalCancel := context.WithCancel(context.Background())
	defer signalCancel()

	go wd.handleSignals(signalCtx, signalCancel)

	for {
		wd.stopMutex.Lock()
		if wd.shouldStop {
			wd.stopMutex.Unlock()
			return nil
		}
		wd.stopMutex.Unlock()

		wd.cmd = exec.Command(bin, args...)
		wd.cmd.Stdout = os.Stdout
		wd.cmd.Stderr = os.Stderr

		log.Println("(INFO): Starting process...")
		if err := wd.cmd.Start(); err != nil {
			log.Printf("(ERROR): Failed to start process: %v\n", err)
			return err
		}

		if err := wd.writePIDToFile(); err != nil {
			log.Printf("(ERROR): Failed to write PID to file: %v\n", err)
			return err
		}

		err := wd.cmd.Wait()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				log.Printf("(WARN): Process exited with error: %v\n", exitErr)
			} else {
				log.Printf("(ERROR): Process failed: %v\n", err)
				return err
			}
		} else {
			log.Println("(INFO): Process completed successfully.")
		}

		wd.stopMutex.Lock()
		if wd.shouldStop {
			wd.stopMutex.Unlock()
			return nil
		}
		wd.stopMutex.Unlock()

		log.Printf("(INFO): Waiting for %s before restart...\n", wd.restartTimeout)
		time.Sleep(wd.restartTimeout)
	}
}

// Stop stops the process and shuts down the Watchdog.
func (wd *Watchdog) Stop() {
	wd.stopMutex.Lock()
	wd.shouldStop = true
	if wd.cmd != nil && wd.cmd.Process != nil {
		log.Println("(INFO): Stopping process...")
		if err := wd.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			log.Printf("(ERROR): Failed to stop process: %v\n", err)
		}
	}
	wd.stopMutex.Unlock()

	wd.doneBarrier.Wait()
	os.RemoveAll(filepath.Dir(wd.pidFile))
	log.Println("(INFO): Watchdog stopped.")
}

// handleSignals listens for OS signals and stops the Watchdog gracefully.
func (wd *Watchdog) handleSignals(ctx context.Context, cancel context.CancelFunc) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-signalChan:
		log.Println("(INFO): Received stop signal.")
		wd.Stop()
		cancel()
	case <-ctx.Done():
		return
	}
}

// writePIDToFile writes the PID of the process to a file.
func (wd *Watchdog) writePIDToFile() error {
	if wd.cmd == nil || wd.cmd.Process == nil {
		return errors.New("process is not running")
	}

	pid := wd.cmd.Process.Pid
	pidData := fmt.Sprintf("%d", pid)

	dir := filepath.Dir(wd.pidFile)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	file, err := os.Create(wd.pidFile)
	if err != nil {
		return fmt.Errorf("failed to create PID file: %v", err)
	}
	defer file.Close()

	_, err = file.WriteString(pidData)
	if err != nil {
		return err
	}

	log.Printf("(INFO): PID %d written to %s\n", pid, wd.pidFile)
	return nil
}
