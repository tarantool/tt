package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/connector"
)

// TestEvalError checks the error of a Lua error of the code: it reads as its
// message and is sdk.ErrEval, wrapped or not.
func TestEvalError(t *testing.T) {
	err := fmt.Errorf("starting the profiler: %w", evalError{message: "x"})
	require.ErrorIs(t, err, sdk.ErrEval)
	require.EqualError(t, evalError{message: "x"}, "x")
	require.NotErrorIs(t, evalError{message: "x"}, sdk.ErrNotFound)
}

// fakeEvaler is a connector.Connector that answers with answer, given the
// expression, the arguments and the result to decode into, and records
// its Close.
type fakeEvaler struct {
	answer func(expr string, args []any, res *[]msgpack.RawMessage) error
	closed chan struct{}
	once   sync.Once
}

func newFakeEvaler(answer func(string, []any, *[]msgpack.RawMessage) error) *fakeEvaler {
	return &fakeEvaler{answer: answer, closed: make(chan struct{}), once: sync.Once{}}
}

func (f *fakeEvaler) Eval(expr string, args []any, opts connector.RequestOpts) ([]any, error) {
	res, ok := opts.ResData.(*[]msgpack.RawMessage)
	if !ok {
		return nil, fmt.Errorf("unexpected ResData %T", opts.ResData)
	}

	return nil, f.answer(expr, args, res)
}

func (f *fakeEvaler) Close() error {
	f.once.Do(func() { close(f.closed) })

	return nil
}

// raw encodes values as the results of an eval.
func raw(t *testing.T, values ...any) []msgpack.RawMessage {
	t.Helper()

	out := make([]msgpack.RawMessage, 0, len(values))

	for _, value := range values {
		data, err := msgpack.Marshal(value)
		require.NoError(t, err)

		out = append(out, data)
	}

	return out
}

// TestDecodeResults pins the Go types of the results of an Eval: integers
// int64, or uint64 above math.MaxInt64, floats float64, strings with their
// bytes, varbinary []byte, arrays []any and every map map[any]any, keys
// included.
func TestDecodeResults(t *testing.T) {
	every := make([]byte, 256)
	for i := range every {
		every[i] = byte(i)
	}

	got, err := decodeResults(raw(t,
		nil, true, int8(-1), uint8(1), uint16(300), int32(-70000), uint64(math.MaxInt64),
		uint64(math.MaxUint64), int64(math.MinInt64), float32(1.5), 2.25,
		string(every), every,
		[]any{uint8(1), "a", []any{}},
		map[string]any{"a": uint8(1)},
		map[any]any{uint8(1): "a", "x": map[any]any{int8(-2): float32(0.5)}},
	))
	require.NoError(t, err)

	assert.Equal(t, []any{
		nil, true, int64(-1), int64(1), int64(300), int64(-70000), int64(math.MaxInt64),
		uint64(math.MaxUint64), int64(math.MinInt64), 1.5, 2.25,
		string(every), every,
		[]any{int64(1), "a", []any{}},
		map[any]any{"a": int64(1)},
		map[any]any{int64(1): "a", "x": map[any]any{int64(-2): 0.5}},
	}, got)

	got, err = decodeResults(nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	// The results as a connector decodes them, into raw messages: that of
	// a nil is empty.
	answer, err := msgpack.Marshal([]any{nil, "msg", nil})
	require.NoError(t, err)

	var messages []msgpack.RawMessage

	require.NoError(t, msgpack.Unmarshal(answer, &messages))

	got, err = decodeResults(messages)
	require.NoError(t, err)
	assert.Equal(t, []any{nil, "msg", nil}, got)

	_, err = decodeResults([]msgpack.RawMessage{{0xc1}})
	require.ErrorContains(t, err, "eval: result 1: ")
}

// TestConnEval checks what a Conn sends and how it reads the answers: the
// chunk of evalChunk with the code before the arguments, the results
// decoded, an error that is not of Lua wrapped and not sdk.ErrEval. The Lua
// errors of the two transports are told in TestConnEvalErrors, on an
// instance.
func TestConnEval(t *testing.T) {
	refused := errors.New("failed to read: connection reset by peer")

	conn := newConn(newFakeEvaler(func(expr string, args []any,
		res *[]msgpack.RawMessage,
	) error {
		require.Equal(t, evalChunk, expr)

		if args[0] == "return ..." {
			*res = raw(t, args[1:]...)

			return nil
		}

		return refused
	}))

	got, err := conn.Eval(t.Context(), "return ...", []any{"a", uint8(2), nil})
	require.NoError(t, err)
	assert.Equal(t, []any{"a", int64(2), nil}, got)

	_, err = conn.Eval(t.Context(), "return box.info", nil)
	require.ErrorIs(t, err, refused)
	require.NotErrorIs(t, err, sdk.ErrEval)

	require.NoError(t, conn.Close())
	require.NoError(t, conn.Close(), "Close of a closed Conn does nothing")

	_, err = conn.Eval(t.Context(), "return ...", nil)
	require.ErrorIs(t, err, errConnClosed)
}

// TestConnEvalContext checks that ctx ends the wait for an answer by closing
// the connection, that the error wraps the error of ctx, and that the Conn
// is closed after it. An Eval waits for the one before it.
func TestConnEvalContext(t *testing.T) {
	var evaler *fakeEvaler

	started := make(chan struct{}, 1)

	evaler = newFakeEvaler(func(string, []any, *[]msgpack.RawMessage) error {
		started <- struct{}{}

		<-evaler.closed

		return errors.New("failed to read: use of closed network connection")
	})

	conn := newConn(evaler)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	_, err := conn.Eval(ctx, "require('fiber').sleep(10)", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	<-started

	_, err = conn.Eval(t.Context(), "return 1", nil)
	require.ErrorIs(t, err, errConnClosed)

	// A second Eval waits for its turn, and a ctx done meanwhile ends the
	// wait without a request.
	conn = newConn(newFakeEvaler(func(string, []any, *[]msgpack.RawMessage) error {
		started <- struct{}{}

		time.Sleep(50 * time.Millisecond)

		return nil
	}))

	first := make(chan error, 1)

	go func() {
		_, err := conn.Eval(t.Context(), "return 1", nil)
		first <- err
	}()

	<-started

	waiting, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()

	_, err = conn.Eval(waiting, "return 2", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, <-first)

	got, err := conn.Eval(t.Context(), "return 3", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	<-started
}

// TestConnectError checks that an application or an instance a lookup does
// not find is sdk.ErrNotFound, keeping the error it was.
func TestConnectError(t *testing.T) {
	for _, missing := range []error{
		app.ErrAppNotFound, app.ErrInstanceNotFound, app.ErrNoPair, app.ErrNoProject,
		app.ErrNoInstances,
	} {
		err := connectError(fmt.Errorf("%w: something", missing))
		require.ErrorIs(t, err, sdk.ErrNotFound, missing)
		require.ErrorIs(t, err, missing)
	}

	other := errors.New("invalid target")
	require.Equal(t, other, connectError(other))
	require.NotErrorIs(t, connectError(app.ErrInvalidTarget), sdk.ErrNotFound)
}
