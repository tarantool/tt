package running

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Instance describes a running process.
type Instance struct {
	// Cmd represents an external command being prepared and run.
	Cmd *exec.Cmd
	// logger represents an active logging object.
	logger *log.Logger
	// tarantoolPath describes the path to the tarantool binary
	// that will be used to launch the Instance.
	tarantoolPath string
	// appPath describes the path to the "init" file of an application.
	appPath string
	// env describes the environment settled by a client.
	env []string
	// consoleSocket is a Unix domain socket to be used as "admin port".
	consoleSocket string
	// waitMutex is used to prevent several invokes of the "Wait"
	// for the same process.
	// https://github.com/golang/go/issues/28461
	waitMutex sync.Mutex
	// done represent whether the instance was stopped.
	done bool
}

// NewInstance creates an Instance.
func NewInstance(tarantoolPath string, appPath string, console_sock string,
	env []string, logger *log.Logger) (*Instance, error) {
	// Check if tarantool binary exists.
	if _, err := exec.LookPath(tarantoolPath); err != nil {
		return nil, err
	}

	// Check if Application exists.
	if _, err := os.Stat(appPath); err != nil {
		return nil, err
	}

	inst := Instance{tarantoolPath: tarantoolPath, appPath: appPath,
		consoleSocket: console_sock, env: env, logger: logger}
	return &inst, nil
}

// SendSignal sends a signal to the Instance.
func (inst *Instance) SendSignal(sig os.Signal) error {
	if inst.Cmd == nil {
		return fmt.Errorf("The instance hasn't started yet.")
	}
	return inst.Cmd.Process.Signal(sig)
}

// IsAlive verifies that the Instance is alive by sending a "0" signal.
func (inst *Instance) IsAlive() bool {
	return inst.SendSignal(syscall.Signal(0)) == nil
}

// Wait calls "Wait" for the process.
func (inst *Instance) Wait() error {
	if inst.done {
		return nil
	}
	// waitMutex is used to prevent several invokes of the "Wait"
	// for the same process.
	// https://github.com/golang/go/issues/28461
	inst.waitMutex.Lock()
	defer inst.waitMutex.Unlock()
	err := inst.Cmd.Wait()
	if err == nil {
		inst.done = true
	}
	return err
}

// Start starts the Instance with the specified parameters.
func (inst *Instance) Start() error {
	// By default (when using "glibc") "stdout" is line buffered when connected
	// to a TTY and block buffered (one page 4KB) when connected to a pipe / file.
	// This is not how we want to log work, so set "stdout" to line buffered mode
	// by using "stdbuf" utility. "strderr" is set to no-buffering by default.
	//
	// Several useful links:
	// https://www.pixelbeat.org/programming/stdio_buffering/
	// https://man7.org/linux/man-pages/man3/setbuf.3.html
	// https://github.com/coreutils/coreutils/blob/master/src/stdbuf.c
	inst.Cmd = exec.Command("stdbuf", "-o", "L",
		inst.tarantoolPath, "-e", instanceLauncher)
	inst.Cmd.Stdout = inst.logger.Writer()
	inst.Cmd.Stderr = inst.logger.Writer()
	inst.Cmd.Env = append(os.Environ(), "TT_CLI_INSTANCE="+inst.appPath)
	inst.Cmd.Env = append(inst.Cmd.Env,
		"TT_CLI_CONSOLE_SOCKET="+inst.consoleSocket)

	// Imitate the "tarantoolctl".
	inst.Cmd.Env = append(inst.Cmd.Env, "TARANTOOLCTL=true")
	// Set the sign that the program is running under "tt".
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_CLI=true")

	// Start an Instance.
	if err := inst.Cmd.Start(); err != nil {
		return err
	}
	inst.done = false

	return nil
}

// Stop terminates the Instance.
//
// timeout - the time that was provided to the process
// to terminate correctly before the "SIGKILL" signal is used.
func (inst *Instance) Stop(timeout time.Duration) error {
	if !inst.IsAlive() {
		return nil
	}

	// Сreate a channel to receive an indication of the termination
	// of the Instance.
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- inst.Wait()
	}()

	// Trying to terminate the process by using a "SIGINT" signal.
	// In case of failure a "SIGKILL" signal will be used.
	if err := inst.SendSignal(os.Interrupt); err != nil {
		return fmt.Errorf("Failed to send SIGINT to instance: %s", err)
	}

	// Terminate the Instance at any cost.
	select {
	case <-time.After(timeout):
		// Send "SIGKILL" signal
		if err := inst.Cmd.Process.Kill(); err != nil {
			return fmt.Errorf("Failed to send SIGKILL to instance: %s", err)
		} else {
			// Wait for the process to terminate.
			_ = <-waitDone
			return nil
		}
	case err := <-waitDone:
		return err
	}
}
