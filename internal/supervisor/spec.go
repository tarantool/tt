package supervisor

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"time"
)

// ErrInvalidSpec is wrapped by the error for a Spec that cannot be started.
var ErrInvalidSpec = errors.New("invalid spec")

// Spec describes one run of the child.
type Spec struct {
	// Path is the executable. A name without a slash is looked up in PATH.
	Path string
	// Args are the arguments, without the program name.
	Args []string
	// Env is the environment; nil inherits the supervisor's environment.
	Env []string
	// Dir is the working directory; empty keeps the supervisor's one.
	Dir string
	// Stdin is written to the child's standard input, which is closed
	// afterwards; nil connects the standard input to the null device.
	Stdin []byte
	// Stdout and Stderr receive the child's output; nil discards it.
	Stdout io.Writer
	Stderr io.Writer
	// StopSignal is the signal that stops the child: it is sent on a stop
	// signal unless ForwardStopSignal is set, and on context cancellation.
	StopSignal syscall.Signal
	// ForwardStopSignal sends the child the stop signal the supervisor
	// received instead of StopSignal.
	ForwardStopSignal bool
	// StopTimeout is how long a stopping child may take before it is killed
	// with SIGKILL. It also bounds how long the output of an exited child is
	// drained.
	StopTimeout time.Duration
	// ProcessGroup starts the child in a process group of its own and sends
	// every signal to that whole group.
	ProcessGroup bool
}

func (spec *Spec) validate() error {
	switch {
	case spec.Path == "":
		return fmt.Errorf("%w: the path is empty", ErrInvalidSpec)
	case spec.StopSignal == 0:
		return fmt.Errorf("%w: the stop signal is not set", ErrInvalidSpec)
	case spec.StopTimeout <= 0:
		return fmt.Errorf("%w: the stop timeout %s is not positive", ErrInvalidSpec,
			spec.StopTimeout)
	}

	return nil
}
