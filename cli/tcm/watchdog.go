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
	Cmd *exec.Cmd
	// Time to wait before restarting the process.
	RestartTimeout time.Duration
	// Flag to indicate if the Watchdog should stop.
	ShouldStop bool
	// Mutex to protect access to ShouldStop.
	StopMutex sync.Mutex
	// WaitGroup to wait for all goroutines to finish.
	DoneBarrier sync.WaitGroup
	// File to store the process PID.
	PidFile string
}

// NewWatchdog creates a new Watchdog instance.
func NewWatchdog(RestartTimeout time.Duration) (*Watchdog, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	filePath := filepath.Join(wd, "tcm/pidFile.pid")

	return &Watchdog{
		RestartTimeout: RestartTimeout,
		PidFile:        filePath,
	}, nil
}


// Start starts the process and monitors its execution.
func (wd *Watchdog) Start(bin string, args ...string) error {
	wd.DoneBarrier.Add(1)
	defer wd.DoneBarrier.Done()

	signalCtx, signalCancel := context.WithCancel(context.Background())
	defer signalCancel()

	go wd.handleSignals(signalCtx, signalCancel)

	for {
		wd.StopMutex.Lock()
		if wd.ShouldStop {
			wd.StopMutex.Unlock()
			return nil
		}
		wd.StopMutex.Unlock()

		wd.Cmd = exec.Command(bin, args...)
		wd.Cmd.Stdout = os.Stdout
		wd.Cmd.Stderr = os.Stderr

		log.Println("(INFO): Starting process...")
		if err := wd.Cmd.Start(); err != nil {
			log.Printf("(ERROR): Failed to start process: %v\n", err)
			return err
		}

		if err := wd.writePIDToFile(); err != nil {
			log.Printf("(ERROR): Failed to write PID to file: %v\n", err)
			return err
		}

		err := wd.Cmd.Wait()
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

		wd.StopMutex.Lock()
		if wd.ShouldStop {
			wd.StopMutex.Unlock()
			return nil
		}
		wd.StopMutex.Unlock()

		log.Printf("(INFO): Waiting for %s before restart...\n", wd.RestartTimeout)
		time.Sleep(wd.RestartTimeout)
	}
}

// Stop stops the process and shuts down the Watchdog.
func (wd *Watchdog) Stop() {
	wd.StopMutex.Lock()

	wd.ShouldStop = true
	if wd.Cmd != nil && wd.Cmd.Process != nil {
		log.Println("(INFO): Stopping process...")
		if err := wd.Cmd.Process.Signal(syscall.SIGTERM); err != nil {
			log.Printf("(ERROR): Failed to stop process: %v\n", err)
		}
	}

	wd.StopMutex.Unlock()

	err := os.Remove(wd.PidFile)
	if err != nil {
		log.Printf("(ERROR): Failed to remove PID file: %v\n", err)
	}

	wd.DoneBarrier.Wait()
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
	if wd.Cmd == nil || wd.Cmd.Process == nil {
		return errors.New("process is not running")
	}

	pid := wd.Cmd.Process.Pid
	pidData := fmt.Sprintf("%d", pid)

	dir := filepath.Dir(wd.PidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	file, err := os.Create(wd.PidFile)
	if err != nil {
		return fmt.Errorf("failed to create PID file: %v", err)
	}
	defer file.Close()

	_, err = file.WriteString(pidData)
	if err != nil {
		return err
	}

	log.Printf("(INFO): PID %d written to %s\n", pid, wd.PidFile)
	return nil
}
