package running

import (
	"context"
	"os"
	"time"
)

// Instance describes a running tarantool instance.
type Instance interface {
	// Start starts the Instance with the specified parameters.
	Start(context.Context) error

	// Run runs tarantool interpreter.
	Run(opts RunOpts) error

	// Wait waits for the process to complete.
	Wait() error

	// SendSignal sends a signal to the process.
	SendSignal(sig os.Signal) error

	// IsAlive verifies that the instance is alive.
	IsAlive() bool

	// Stop terminates the process.
	//
	// waitTimeout - the time that was provided to the process
	// to terminate correctly before killing it.
	Stop(waitTimeout time.Duration) error

	// StopWithSignal terminates the process with specific signal.
	StopWithSignal(waitTimeout time.Duration, usedSignal os.Signal) error

	// GetPid returns instance process PID.
	GetPid() int

	// ProcessState returns completed process state.
	ProcessState() *os.ProcessState
}
