package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	// ErrInvalidOptions is wrapped by the error New returns for options it
	// refuses.
	ErrInvalidOptions = errors.New("invalid supervisor options")
	// ErrAlreadyRun is returned by a second call to Engine.Run.
	ErrAlreadyRun = errors.New("the supervisor has already run")
	// ErrCheckPanicked is wrapped by the error of a check that panicked; it
	// counts as a failed check.
	ErrCheckPanicked = errors.New("the check panicked")
	// errUnknownSignal is reported for a signal that is not a syscall.Signal.
	errUnknownSignal = errors.New("not a system signal")
)

// Source decides what the engine runs and whether it runs it again. Its
// methods are called from the goroutine running Engine.Run.
type Source interface {
	// Next returns the Spec for the next start. It is called before every
	// start, including the first. An error ends Run.
	Next(ctx context.Context) (Spec, error)
	// Restart reports whether to start the child again after it exited on
	// its own. It is not called when the exit followed a stop or a failed
	// check. An error ends Run.
	Restart(ctx context.Context, exit Exit) (bool, error)
}

// Options configure an Engine. A zero field means "none", except for Start.
type Options struct {
	// PidFile and ChildPidFile are the pid files of the supervisor and of the
	// child; the package documentation states when each is written.
	PidFile      string
	ChildPidFile string
	// StopSignals are the received signals that stop the child and end Run.
	StopSignals []syscall.Signal
	// IgnoreSignals are the received signals that are dropped: never
	// forwarded, never stopping anything.
	IgnoreSignals []syscall.Signal
	// ReloadSignal is the received signal that runs OnReload before it is
	// forwarded. It is set together with OnReload.
	ReloadSignal syscall.Signal
	// OnReload runs on ReloadSignal, for example to reopen a log file.
	OnReload func() error
	// RestartDelay is the pause between an exit and the next start.
	RestartDelay time.Duration
	// CheckPeriod is how often Check runs while a child is running. It is set
	// together with Check.
	CheckPeriod time.Duration
	// Check is the periodic check. It runs on a goroutine of its own; an
	// error or a panic kills the child and ends Run, which never restarts
	// it.
	Check func(ctx context.Context) error
	// Cleanup runs once when Run returns, before the supervisor pid file is
	// removed, unless that file could not be created.
	Cleanup func()
	OnEvent func(event Event)
	// Start starts every child; nil means StartCmd.
	Start StartFunc
}

// Op is the step of the supervision an Error comes from.
type Op int

const (
	// OpPidFile is creating or removing the supervisor pid file.
	OpPidFile Op = iota + 1
	// OpNext is getting a valid Spec from the Source.
	OpNext
	// OpStart is starting the child.
	OpStart
	// OpChildPidFile is writing or removing the child pid file.
	OpChildPidFile
	// OpRestart is asking the Source about a restart.
	OpRestart
	// OpCheck is the periodic check.
	OpCheck
)

func (op Op) String() string {
	switch op {
	case OpPidFile:
		return "the supervisor pid file"
	case OpNext:
		return "preparing the child"
	case OpStart:
		return "starting the child"
	case OpChildPidFile:
		return "the child pid file"
	case OpRestart:
		return "deciding on a restart"
	case OpCheck:
		return "the periodic check"
	}

	return "unknown step"
}

// Error is an error that ended Engine.Run, with the step it came from.
type Error struct {
	Op  Op
	Err error
}

func (err *Error) Error() string {
	return fmt.Sprintf("%s: %s", err.Op, err.Err)
}

func (err *Error) Unwrap() error {
	return err.Err
}

// Engine supervises one child at a time. See the package documentation for
// the model.
type Engine struct {
	source Source
	opts   Options
	// subscribe starts delivering signals; tests replace it to inject them.
	subscribe func() (<-chan os.Signal, func())
	// spawn starts a child; tests replace it with processes they drive.
	spawn func(ctx context.Context, spec *Spec) (*child, error)
	// clock makes the timers; tests replace it with one they advance.
	clock clock
	ran   atomic.Bool
}

// New checks the options and returns an engine that is ready to Run.
func New(source Source, opts Options) (*Engine, error) {
	err := validateOptions(source, &opts)
	if err != nil {
		return nil, err
	}

	start := opts.Start
	if start == nil {
		start = StartCmd
	}

	return &Engine{
		source:    source,
		opts:      opts,
		subscribe: subscribeOS,
		spawn: func(ctx context.Context, spec *Spec) (*child, error) {
			return startChild(ctx, spec, start)
		},
		clock: realClock{},
		ran:   atomic.Bool{},
	}, nil
}

// uncatchable reports a signal the engine never receives.
func uncatchable(sig syscall.Signal) bool {
	switch sig {
	case syscall.SIGKILL, syscall.SIGSTOP, syscall.SIGURG, syscall.SIGCHLD:
		return true
	default:
		return false
	}
}

func validateOptions(source Source, opts *Options) error {
	if source == nil {
		return fmt.Errorf("%w: no source", ErrInvalidOptions)
	}

	for _, sig := range opts.StopSignals {
		if sig == 0 || uncatchable(sig) {
			return fmt.Errorf("%w: %s cannot be a stop signal", ErrInvalidOptions, sig)
		}
	}

	for _, sig := range opts.IgnoreSignals {
		switch {
		case sig == 0 || uncatchable(sig):
			return fmt.Errorf("%w: %s cannot be ignored", ErrInvalidOptions, sig)
		case slices.Contains(opts.StopSignals, sig) || sig == opts.ReloadSignal:
			return fmt.Errorf("%w: %s is both ignored and handled", ErrInvalidOptions, sig)
		}
	}

	switch {
	case (opts.ReloadSignal == 0) != (opts.OnReload == nil):
		return fmt.Errorf("%w: the reload signal and hook go together", ErrInvalidOptions)
	case uncatchable(opts.ReloadSignal):
		return fmt.Errorf("%w: %s cannot be the reload signal", ErrInvalidOptions,
			opts.ReloadSignal)
	case slices.Contains(opts.StopSignals, opts.ReloadSignal):
		return fmt.Errorf("%w: %s is both a stop and the reload signal", ErrInvalidOptions,
			opts.ReloadSignal)
	case opts.RestartDelay < 0:
		return fmt.Errorf("%w: negative restart delay", ErrInvalidOptions)
	case opts.CheckPeriod < 0:
		return fmt.Errorf("%w: negative check period", ErrInvalidOptions)
	case (opts.CheckPeriod == 0) != (opts.Check == nil):
		return fmt.Errorf("%w: the check period and the check go together", ErrInvalidOptions)
	}

	return nil
}
