package running

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var (
	errTheInstanceHasnTStartedYet = errors.New("the instance hasn't started yet")
)

// newProcessController create new process controller.
func newProcessController(cmd *exec.Cmd) (*processController, error) {
	dpc := processController{Cmd: cmd, waitMutex: sync.Mutex{}, done: false}

	err := dpc.start()
	if err != nil {
		return nil, err
	}

	return &dpc, nil
}

// processController represents a command being run.
type processController struct {
	// Cmd represents an external command to run.
	*exec.Cmd

	// waitMutex is used to prevent several invokes of the "Wait"
	// for the same process.
	// https://github.com/golang/go/issues/28461
	waitMutex sync.Mutex
	// done represent whether the process was stopped.
	done bool
}

// Wait waits for the process to complete.
func (pc *processController) Wait() error {
	if pc.done {
		return nil
	}

	// waitMutex is used to prevent several invokes of the "Wait"
	// for the same process.
	// https://github.com/golang/go/issues/28461
	pc.waitMutex.Lock()
	defer pc.waitMutex.Unlock()

	err := pc.Cmd.Wait()
	if err != nil {
		return fmt.Errorf("waiting for the process: %w", err)
	}

	pc.done = true

	return nil
}

// SendSignal sends a signal to tarantool instance.
func (pc *processController) SendSignal(sig os.Signal) error {
	if pc.Cmd == nil || pc.Process == nil {
		return errTheInstanceHasnTStartedYet
	}

	err := pc.Process.Signal(sig)
	if err != nil {
		return fmt.Errorf("failed to send %v to instance: %w", sig, err)
	}

	return nil
}

// IsAlive verifies that the Instance is alive by sending a "0" signal.
func (pc *processController) IsAlive() bool {
	if pc.done {
		return false
	}

	return pc.SendSignal(syscall.Signal(0)) == nil
}

func (pc *processController) Stop(waitTimeout time.Duration) error {
	return pc.StopWithSignal(waitTimeout, os.Interrupt)
}

// StopWithSignal sends the signal to the process and waits for it to complete.
//
// timeout - the time to wait for a process to complete before the "SIGKILL" signal to be sent.
func (pc *processController) StopWithSignal(waitTimeout time.Duration, stopSignal os.Signal) error {
	if !pc.IsAlive() {
		return nil
	}

	// Create a channel to receive an indication of the termination
	// of the Instance.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- pc.Wait()
	}()

	// Trying to terminate the process by using a stopSignal.
	// In case of failure a "SIGKILL" signal will be used.
	err := pc.SendSignal(stopSignal)
	if err != nil {
		return err
	}

	// Terminate the process at any cost.
	select {
	case <-time.After(waitTimeout):
		if pc.IsAlive() {
			// Send "SIGKILL" signal if process is still alive.
			err = pc.Process.Kill()
			if err != nil {
				return fmt.Errorf("failed to send SIGKILL to instance: %w", err)
			}

			// Wait for the process to terminate.
			<-waitDone

			return nil
		}
	case err := <-waitDone:
		return err
	}

	return nil
}

// GetPid returns process PID.
func (pc *processController) GetPid() int {
	return pc.Process.Pid
}

// ProcessState returns completed process state.
func (pc *processController) ProcessState() *os.ProcessState {
	return pc.Cmd.ProcessState
}

// start starts the process.
func (pc *processController) start() error {
	// Start an Instance.
	err := pc.Start()
	if err != nil {
		return fmt.Errorf("starting the process: %w", err)
	}

	pc.done = false

	return nil
}
