package running

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/tarantool/tt/cli/ttlog"
	"golang.org/x/sys/unix"
)

// Instance describes a running process.
type Instance struct {
	// Cmd represents an external command being prepared and run.
	Cmd *exec.Cmd
	// logger represents an active logging object.
	logger *ttlog.Logger
	// tarantoolPath describes the path to the tarantool binary
	// that will be used to launch the Instance.
	tarantoolPath string
	// appPath describes the path to the "init" file of an application.
	appPath string
	// dataDir describes the path to the directory
	// where wal, vinyl and memtx files are stored.
	dataDir string
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
	env []string, logger *ttlog.Logger, dataDir string) (*Instance, error) {
	// Check if tarantool binary exists.
	if _, err := exec.LookPath(tarantoolPath); err != nil {
		return nil, err
	}

	// Check if Application exists.
	if _, err := os.Stat(appPath); err != nil {
		return nil, err
	}

	inst := Instance{tarantoolPath: tarantoolPath, appPath: appPath,
		consoleSocket: console_sock, env: env, logger: logger,
		dataDir: dataDir}
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
	inst.Cmd = exec.Command(inst.tarantoolPath, "-")
	inst.Cmd.Stdout = inst.logger.Writer()
	inst.Cmd.Stderr = inst.logger.Writer()
	StdinPipe, err := inst.Cmd.StdinPipe()
	if err != nil {
		return err
	}
	inst.Cmd.Env = append(os.Environ(), "TT_CLI_INSTANCE="+inst.appPath)
	inst.Cmd.Env = append(inst.Cmd.Env,
		"TT_CLI_CONSOLE_SOCKET="+inst.consoleSocket)

	// Imitate the "tarantoolctl".
	inst.Cmd.Env = append(inst.Cmd.Env, "TARANTOOLCTL=true")
	// Set the sign that the program is running under "tt".
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_CLI=true")
	// Set the wal, memtx and vinyls dirs.
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_VINYL_DIR="+inst.dataDir)
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_WAL_DIR="+inst.dataDir)
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_MEMTX_DIR="+inst.dataDir)

	// Start an Instance.
	if err := inst.Cmd.Start(); err != nil {
		return err
	}
	StdinPipe.Write([]byte(instanceLauncher))
	StdinPipe.Close()
	inst.done = false

	return nil
}

// makeRunCommand turns flags into tarantool command.
func makeRunCommand(flags *RunFlags) []string {
	var command []string
	if flags.RunEval != "" {
		command = append(command, "-e")
		command = append(command, flags.RunEval)
	}
	if flags.RunInteractive {
		command = append(command, "-i")
	}
	if flags.RunLib != "" {
		command = append(command, "-l")
		command = append(command, flags.RunLib)
	}
	if flags.RunVersion {
		command = append(command, "-v")
	}
	return command
}

// Run runs tarantool instance.
func (inst *Instance) Run(flags *RunFlags) error {
	command := makeRunCommand(flags)
	// pipeFlag is a flag used to indicate whether stdin
	// should be moved or not.
	// It is used in cases when calling tarantool with "-" flag to hide input
	// for example from ps|ax.
	// e.g ./tt run - ... or test.lua | ./tt run -
	pipeFlag := false
	stdinFdNum := 0
	inst.Cmd = exec.Command(inst.tarantoolPath)
	if inst.appPath == "" && flags.RunStdin == "" {
		if len(command) != 0 {
			inst.Cmd.Args = append(inst.Cmd.Args, command...)
		}
	} else {
		if len(command) == 0 && flags.RunStdin == "" {
			inst.Cmd.Args = append(inst.Cmd.Args, "-")
		} else {
			inst.Cmd.Args = append(inst.Cmd.Args, command...)
			inst.Cmd.Args = append(inst.Cmd.Args, "-")
		}
		// Move stdin to different Fd to write data
		// passed through "-" flag or pipe.
		stdinFdNum, _ = unix.FcntlInt(os.Stdin.Fd(), unix.F_DUPFD, 0)
		pipeFlag = true
	}

	if len(flags.RunArgs) != 0 {
		for i := 0; i < len(flags.RunArgs); i++ {
			inst.Cmd.Args = append(inst.Cmd.Args, flags.RunArgs[i])
		}
	}

	inst.Cmd.Stdout = os.Stdout
	inst.Cmd.Stderr = os.Stderr

	inst.Cmd.Env = append(os.Environ(), "TT_CLI_INSTANCE="+inst.appPath)
	// Set the sign that the program is running under "tt".
	inst.Cmd.Env = append(inst.Cmd.Env, "TT_CLI=true")

	if pipeFlag {
		inst.Cmd.Env = append(inst.Cmd.Env, "TT_CLI_RUN_STDIN_FD="+fmt.Sprint(stdinFdNum))
		StdinPipe, err := inst.Cmd.StdinPipe()
		if err != nil {
			return err
		}
		if inst.appPath == "" {
			StdinPipe.Write([]byte(flags.RunStdin))
		} else {
			StdinPipe.Write([]byte(instanceLauncher))
		}
		StdinPipe.Close()
	} else {
		inst.Cmd.Stdin = os.Stdin
	}

	// Start an Instance.
	if err := inst.Cmd.Start(); err != nil {
		return err
	}

	err := inst.Cmd.Wait()
	if err != nil {
		return err
	}

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

	// ??reate a channel to receive an indication of the termination
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
