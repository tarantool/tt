package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/ttlog"
)

const (
	EnvName = "TT_CLI_DAEMON"
)

const (
	stopDaemonMsg  = "Stopping tt daemon..."
	startDaemonMsg = "Starting tt daemon..."
)

// Worker describes an interface of
// a worker the process will manage.
type Worker interface {
	Start(ttPath string)
	Stop() error
	SetLogger(logger *ttlog.Logger)
}

// Process describes a running process.
type Process struct {
	// logOpts are options to create a logger.
	logOpts ttlog.LoggerOpts
	// logger is a log file the process will write to.
	logger *ttlog.Logger
	// pidFileName is a path to the process pid file.
	pidFileName string
	// cmdPath is a path to the command the process should perform.
	cmdPath string
	// cmdArgs are arguments to the command the process should perform.
	cmdArgs []string
	// worker is a worker the process will manage.
	worker Worker
	// DaemonTag is an env name to check the process is a child process.
	DaemonTag string
}

// startSignalHandling adds "SIGTERM" and "SIGINT"
// signal handling to terminate gracefully.
func (process *Process) startSignalHandling() {
	sigTermChan := make(chan os.Signal, 1)
	signal.Notify(sigTermChan, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case _ = <-sigTermChan:
			process.Stop()
		}
	}
}

// NewProcess creates new Process.
func NewProcess(worker Worker, pidFileName string, logOpts ttlog.LoggerOpts) *Process {
	process := &Process{
		logOpts:     logOpts,
		pidFileName: pidFileName,
		worker:      worker,
		DaemonTag:   EnvName,
		cmdPath:     os.Args[0],
		cmdArgs:     os.Args[1:],
	}

	return process
}

// CmdPath sets a path to the command the process should perform.
func (process *Process) CmdPath(cmdPath string) *Process {
	process.cmdPath = cmdPath
	return process
}

// CmdArgs sets arguments to the command the process should perform.
func (process *Process) CmdArgs(cmdArgs []string) *Process {
	process.cmdArgs = cmdArgs
	return process
}

// IsChild checks a process is the child process.
func (process *Process) IsChild() bool {
	return os.Getenv(process.DaemonTag) == "true"
}

// Start starts the process.
func (process *Process) Start() error {
	if process.IsChild() {
		process.logger = ttlog.NewLogger(process.logOpts)

		if err := process_utils.CreatePIDFile(process.pidFileName); err != nil {
			return err
		}

		process.logger.Println(startDaemonMsg)
		process.worker.SetLogger(process.logger)

		go process.worker.Start(process.cmdPath)
		process.startSignalHandling()
		return nil
	}

	if err := process_utils.CheckPIDFile(process.pidFileName); err != nil {
		return err
	}

	cmd := exec.Command(process.cmdPath, process.cmdArgs...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=true", process.DaemonTag))

	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Process.Release()
}

// Stop stops the process.
func (process *Process) Stop() {
	process.logger.Println(stopDaemonMsg)
	done := make(chan error, 1)
	go func() {
		done <- process.worker.Stop()
	}()

	os.Remove(process.pidFileName)
	_ = os.Unsetenv(process.DaemonTag)

	err := <-done

	if err != nil {
		process.logger.Println(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}
