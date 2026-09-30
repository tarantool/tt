package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// ErrNotStarted is returned when a StartFunc reports success without having
// started the process.
var ErrNotStarted = errors.New("the start function returned without a process")

// StartFunc starts a fully prepared command, the way exec.Cmd.Start does:
// on success cmd.Process is the running process, and the caller waits for it
// with cmd.Wait. It may change how the process is started, as long as the
// process started is a child of the caller. It starts one process, through
// the command it is given: a process started through another command, or
// before the command is replaced, is not known to the engine and is neither
// stopped nor waited for.
type StartFunc func(cmd *exec.Cmd) error

// StartCmd is the StartFunc that calls cmd.Start.
func StartCmd(cmd *exec.Cmd) error {
	err := cmd.Start()
	if err != nil {
		return fmt.Errorf("starting %q: %w", cmd.Path, err)
	}

	return nil
}

type child struct {
	pid int
	// done receives the exit of the child exactly once.
	done chan Exit
	// send delivers a signal to the child, or to its process group.
	send func(sig syscall.Signal) error
}

func command(ctx context.Context, spec *Spec) *exec.Cmd {
	// The engine stops the child itself; the context must not kill it.
	return spec.Command(context.WithoutCancel(ctx))
}

// Command prepares a command that runs the Spec once, for a caller that runs
// it without a supervisor. Cancelling ctx sends the process Spec.StopSignal;
// Spec.StopTimeout later the process is killed. The output of the process is
// drained for at most Spec.StopTimeout after it has exited.
func (spec *Spec) Command(ctx context.Context) *exec.Cmd {
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)

	cmd.Env = spec.Env
	cmd.Dir = spec.Dir

	if spec.Stdin != nil {
		cmd.Stdin = bytes.NewReader(spec.Stdin)
	}

	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: spec.ProcessGroup}
	cmd.WaitDelay = spec.StopTimeout
	cmd.Cancel = func() error {
		return cmd.Process.Signal(spec.StopSignal)
	}

	return cmd
}

func startChild(ctx context.Context, spec *Spec, start StartFunc) (*child, error) {
	cmd := command(ctx, spec)
	handedOver := false

	// A StartFunc that started the process and then failed or panicked has
	// not handed it over: nobody else knows it, so it is killed and waited
	// for here, before the error or the panic goes on.
	defer func() {
		if !handedOver && cmd.Process != nil {
			_ = signaller(cmd.Process, spec.ProcessGroup)(syscall.SIGKILL)
			_ = cmd.Wait()
		}
	}()

	err := start(cmd)
	if err != nil {
		return nil, err
	}

	if cmd.Process == nil {
		return nil, ErrNotStarted
	}

	proc := &child{
		pid:  cmd.Process.Pid,
		done: make(chan Exit, 1),
		send: signaller(cmd.Process, spec.ProcessGroup),
	}

	go func() {
		err := cmd.Wait()
		proc.done <- Exit{Pid: proc.pid, State: cmd.ProcessState, Err: err}
	}()

	handedOver = true

	return proc, nil
}

// signaller sends signals to process, or to the process group it leads.
func signaller(process *os.Process, group bool) func(sig syscall.Signal) error {
	return func(sig syscall.Signal) error {
		if group {
			err := syscall.Kill(-process.Pid, sig)
			if err != nil {
				return fmt.Errorf("sending %s to the process group %d: %w", sig, process.Pid,
					err)
			}

			return nil
		}

		err := process.Signal(sig)
		if err != nil {
			return fmt.Errorf("sending %s to the process %d: %w", sig, process.Pid, err)
		}

		return nil
	}
}

func (proc *child) signal(sig syscall.Signal) error {
	return proc.send(sig)
}
