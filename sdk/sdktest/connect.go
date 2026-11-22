package sdktest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/tarantool/tt/sdk"
)

// errConnClosed reports an Eval of a Conn of the fake that is closed.
var errConnClosed = errors.New("sdktest: the connection is closed")

// EvalFunc answers the Evals of a Conn the fake Connect opens: it is given
// the context, the code and the arguments of the Eval, and what it returns
// the Eval returns. A Lua error of the code is [EvalError]; a function that
// waits for ctx makes an Eval that ctx ends.
type EvalFunc func(ctx context.Context, expr string, args []any) ([]any, error)

// WithInstance makes Connect of target succeed: the Conn it returns answers
// every Eval with eval. The target is compared as it is given, so the test
// and the module must name the instance the same way, such as
// "app:storage-1". A target given neither an instance nor an error
// ([WithConnectError]) is not found: Connect fails with an error wrapping
// sdk.ErrNotFound, as the core's does for an application or an instance it
// does not find.
func WithInstance(target string, eval EvalFunc) Option {
	return func(o *options) { o.instances[target] = eval }
}

// WithConnectError makes Connect of target fail with err, as the core's
// fails for an instance that is not running or a URI nothing listens on.
func WithConnectError(target string, err error) Option {
	return func(o *options) { o.connectErrs[target] = err }
}

// EvalError returns the error Conn.Eval returns for a Lua error with
// message that the code raised, as the core's does: it reads as message and
// wraps sdk.ErrEval. An EvalFunc returns it for a Lua error.
func EvalError(message string) error {
	return evalError{message: message}
}

// evalError is a Lua error the code of an Eval raised.
type evalError struct {
	message string
}

// Error returns the message of the Lua error.
func (e evalError) Error() string { return e.message }

// Is reports whether target is sdk.ErrEval.
func (evalError) Is(target error) bool { return target == sdk.ErrEval }

// ConnectCall is a call of Services.Connect ([Services.Connects]).
type ConnectCall struct {
	// Target is the target it was given.
	Target string
	// Options are the options it was given.
	Options sdk.ConnectOptions
}

// EvalCall is a call of Conn.Eval of a Conn the fake opened
// ([Services.Evals]).
type EvalCall struct {
	// Target is the target the Conn was opened to.
	Target string
	// Expr is the code.
	Expr string
	// Args are the arguments.
	Args []any
}

// Connect returns a Conn to target that answers as WithInstance said, or
// fails as WithConnectError said, or with an error wrapping sdk.ErrNotFound.
// No connection is made and the options are not checked; the call is
// recorded either way ([Services.Connects]).
func (s *Services) Connect(
	ctx context.Context, target string, opts sdk.ConnectOptions,
) (sdk.Conn, error) {
	s.ready("Connect")

	s.mu.Lock()
	defer s.mu.Unlock()

	s.connects = append(s.connects, ConnectCall{Target: target, Options: opts})

	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("connecting to %q: %w", target, err)
	}

	err, failed := s.opts.connectErrs[target]
	if failed {
		return nil, err
	}

	eval, ok := s.opts.instances[target]
	if !ok {
		return nil, fmt.Errorf("instance %q: %w", target, sdk.ErrNotFound)
	}

	s.conns++

	s.open++

	return &fakeConn{
		services: s, target: target, eval: eval, turn: sync.Mutex{}, closed: atomic.Bool{},
	}, nil
}

// Connects returns the calls of Connect so far, in order, the failed ones
// included.
func (s *Services) Connects() []ConnectCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.connects)
}

// Evals returns the calls of Eval of the Conns Connect opened so far, in
// order, the failed ones included.
func (s *Services) Evals() []EvalCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	evals := make([]EvalCall, 0, len(s.evals))
	for _, call := range s.evals {
		call.Args = slices.Clone(call.Args)
		evals = append(evals, call)
	}

	return evals
}

// Conns returns how many Conns Connect opened so far and how many of them
// are not closed, for a test that checks that a command closes what it
// opens.
func (s *Services) Conns() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.conns, s.open
}

// fakeConn is a Conn the fake Connect opened.
type fakeConn struct {
	services *Services
	target   string
	eval     EvalFunc
	// turn runs the Evals one at a time.
	turn sync.Mutex
	// closed is set once the Conn is closed.
	closed atomic.Bool
}

// Eval records the call and answers it with the EvalFunc of the target. A
// closed Conn fails it. When ctx is done, before the call or with the call
// failed, the Conn is closed and the error wraps ctx.Err(), as the core's
// does.
func (c *fakeConn) Eval(ctx context.Context, expr string, args []any) ([]any, error) {
	c.turn.Lock()
	defer c.turn.Unlock()

	c.services.recordEval(EvalCall{Target: c.target, Expr: expr, Args: slices.Clone(args)})

	if c.closed.Load() {
		return nil, fmt.Errorf("eval on %q: %w", c.target, errConnClosed)
	}

	if ctx.Err() != nil {
		c.close()

		return nil, fmt.Errorf("eval on %q: %w", c.target, ctx.Err())
	}

	results, err := c.eval(ctx, expr, slices.Clone(args))
	if err != nil && ctx.Err() != nil {
		c.close()

		return nil, fmt.Errorf("eval on %q: %w", c.target, ctx.Err())
	}

	return results, err
}

// Close closes the Conn, while an Eval runs too; Close of a closed one does
// nothing.
func (c *fakeConn) Close() error {
	c.close()

	return nil
}

// close closes the Conn the first time it is called.
func (c *fakeConn) close() {
	if c.closed.CompareAndSwap(false, true) {
		c.services.closeConn()
	}
}

// recordEval records a call of Conn.Eval.
func (s *Services) recordEval(call EvalCall) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.evals = append(s.evals, call)
}

// closeConn counts a Conn closed.
func (s *Services) closeConn() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.open--
}
