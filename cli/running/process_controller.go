package running

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// newProcessController create new process controller.
func newProcessController(cmd *exec.Cmd) (*processController, error) {
	dpc := processController{Cmd: cmd}
	if err := dpc.start(); err != nil {
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

// start starts the process.
func (pc *processController) start() error {
	// Start an Instance.
	if err := pc.Cmd.Start(); err != nil {
		return err
	}
	pc.done = false
	return nil
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
	if err == nil {
		pc.done = true
	}
	return err
}

// SendSignal sends a signal to tarantool instance.
func (pc *processController) SendSignal(sig os.Signal) error {
	if pc.Cmd == nil || pc.Cmd.Process == nil {
		return fmt.Errorf("the instance hasn't started yet")
	}
	return pc.Cmd.Process.Signal(sig)
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

	// Сreate a channel to receive an indication of the termination
	// of the Instance.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- pc.Wait()
	}()

	// Trying to terminate the process by using a stopSignal.
	// In case of failure a "SIGKILL" signal will be used.
	if err := pc.SendSignal(stopSignal); err != nil {
		return fmt.Errorf("failed to send %v to instance: %s", stopSignal, err)
	}

	// Terminate the process at any cost.
	select {
	case <-time.After(waitTimeout):
		// Send "SIGKILL" signal
		if err := pc.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to send SIGKILL to instance: %s", err)
		} else {
			// Wait for the process to terminate.
			<-waitDone
			return nil
		}
	case err := <-waitDone:
		return err
	}
}

// GetPid returns process PID.
func (pc *processController) GetPid() int {
	return pc.Process.Pid
}

// ProcessState returns completed process state.
func (pc *processController) ProcessState() *os.ProcessState {
	return pc.Cmd.ProcessState
}
