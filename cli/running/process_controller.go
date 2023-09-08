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
	dpc := processController{cmd: cmd}
	if err := dpc.start(); err != nil {
		return nil, err
	}
	return &dpc, nil
}

// processController represents a command being run.
type processController struct {
	// Cmd represents an external command to run.
	cmd *exec.Cmd
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
	if err := pc.cmd.Start(); err != nil {
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
	err := pc.cmd.Wait()
	if err == nil {
		pc.done = true
	}
	return err
}

// SendSignal sends a signal to tarantool instance.
func (pc *processController) SendSignal(sig os.Signal) error {
	if pc.cmd == nil || pc.cmd.Process == nil {
		return fmt.Errorf("the instance hasn't started yet")
	}
	return pc.cmd.Process.Signal(sig)
}

// IsAlive verifies that the Instance is alive by sending a "0" signal.
func (pc *processController) IsAlive() bool {
	if pc.done {
		return false
	}

	return pc.SendSignal(syscall.Signal(0)) == nil
}

// Stop terminates the process.
//
// timeout - the time that was provided to the process
// to terminate correctly before the "SIGKILL" signal is used.
func (pc *processController) Stop(waitTimeout time.Duration) error {
	if !pc.IsAlive() {
		return nil
	}

	// Сreate a channel to receive an indication of the termination
	// of the Instance.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- pc.Wait()
	}()

	// Trying to terminate the process by using a "SIGINT" signal.
	// In case of failure a "SIGKILL" signal will be used.
	if err := pc.SendSignal(os.Interrupt); err != nil {
		return fmt.Errorf("failed to send SIGINT to instance: %s", err)
	}

	// Terminate the process at any cost.
	select {
	case <-time.After(waitTimeout):
		// Send "SIGKILL" signal
		if err := pc.cmd.Process.Kill(); err != nil {
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
