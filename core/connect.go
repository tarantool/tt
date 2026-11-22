package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/connector"
)

// errConnClosed reports an Eval of a Conn that is closed.
var errConnClosed = errors.New("the connection is closed")

// evalChunk is the chunk a Conn runs for an Eval, on the console and over
// iproto alike: the code of the Eval and its arguments are its arguments.
// It runs the code as a chunk of its own, so that the code is the same
// whatever the transport does with the chunk that carries it, and makes
// what the two transports answer with the same: the transports encode a
// result and an error each its own way.
//
// A Lua error of the code is raised again as a string, its message without
// the position of the chunk in front of it, eval:<line>, and with no
// position of its own (level 0), so that iproto, which answers with the
// message of the error as it is, and the console, which answers with
// tostring of it, give the same message.
//
// A nil result is box.NULL: the console encodes the results as one table,
// where a nil would leave a hole, or end it, and iproto encodes each of them
// in its place.
//
//nolint:dupword // Lua ends two blocks with "end end".
const evalChunk = `local code = ...
local chunk, err = load(code, '=eval')
if chunk == nil then
    error('the code does not compile: ' .. err, 0)
end

local function pack(...)
    return {n = select('#', ...), ...}
end

-- The code, given the arguments after it.
local ret = pack(pcall(chunk, select(2, ...)))
if not ret[1] then
    local message = tostring(ret[2]):gsub('^eval:%d+: ', '')
    error(message, 0)
end

for i = 2, ret.n do
    if ret[i] == nil then
        ret[i] = box.NULL
    end
end

return unpack(ret, 2, ret.n)
`

// Connect connects to target as tt connect does without --binary
// (cmd.ConnectTarget). An application or an instance the lookup does not
// find is sdk.ErrNotFound as well.
func (s *services) Connect(
	ctx context.Context, target string, opts sdk.ConnectOptions,
) (sdk.Conn, error) {
	s.mustBeReady("Connect")

	evaler, err := cmd.ConnectTarget(ctx, target, opts)
	if err != nil {
		return nil, connectError(err)
	}

	return newConn(evaler), nil
}

// connectError returns err, the error of a connection to a target, as the
// SDK has it: an application or an instance not found is sdk.ErrNotFound as
// well.
func connectError(err error) error {
	for _, missing := range []error{
		app.ErrAppNotFound, app.ErrInstanceNotFound, app.ErrNoPair, app.ErrNoProject,
		app.ErrNoInstances,
	} {
		if errors.Is(err, missing) {
			return notFoundError{err: err}
		}
	}

	return err
}

// conn is a connection Services.Connect opened: an evaler of the console or
// of iproto, which runs the chunk of an Eval, evalChunk, one at a time.
type conn struct {
	// evaler is the connection.
	evaler connector.Connector
	// turn is taken by the Eval that runs.
	turn chan struct{}
	// closed is set once the connection is closed.
	closed atomic.Bool
	// closeOnce closes the connection once.
	closeOnce sync.Once
}

// newConn returns the Conn of evaler.
func newConn(evaler connector.Connector) *conn {
	return &conn{
		evaler:    evaler,
		turn:      make(chan struct{}, 1),
		closed:    atomic.Bool{},
		closeOnce: sync.Once{},
	}
}

// Eval runs expr with args in evalChunk and decodes its results. When ctx is
// done the connection is closed, which ends the wait of the evaler for the
// answer.
func (c *conn) Eval(ctx context.Context, expr string, args []any) ([]any, error) {
	select {
	case c.turn <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("eval: %w", ctx.Err())
	}

	defer func() { <-c.turn }()

	if c.closed.Load() {
		return nil, fmt.Errorf("eval: %w", errConnClosed)
	}

	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}

	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()

	var raw []msgpack.RawMessage

	_, err = c.evaler.Eval(evalChunk, append([]any{expr}, args...), connector.RequestOpts{
		PushCallback: nil, ReadTimeout: 0, ResData: &raw,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("eval: %w", ctx.Err())
		}

		message, ok := connector.LuaError(err)
		if ok {
			return nil, evalError{message: message}
		}

		return nil, fmt.Errorf("eval: %w", err)
	}

	return decodeResults(raw)
}

// Close closes the connection the first time it is called.
func (c *conn) Close() error {
	var err error

	c.closeOnce.Do(func() {
		c.closed.Store(true)

		err = c.evaler.Close()
	})

	return err
}

// evalError is a Lua error the code of an Eval raised: it reads as its
// message and is sdk.ErrEval.
type evalError struct {
	message string
}

// Error returns the message of the Lua error.
func (e evalError) Error() string { return e.message }

// Is reports whether target is sdk.ErrEval.
func (evalError) Is(target error) bool { return target == sdk.ErrEval }

// decodeResults decodes the results of an Eval, each a MessagePack value as
// the instance encoded it, into the values Conn.Eval returns, the same
// whichever transport carried them.
func decodeResults(raw []msgpack.RawMessage) ([]any, error) {
	results := make([]any, 0, len(raw))

	for index, value := range raw {
		// The decoder leaves the raw message of a nil empty, as it leaves
		// any slice it decodes a nil into; a value has a byte at least.
		if len(value) == 0 {
			results = append(results, nil)

			continue
		}

		decoder := msgpack.NewDecoder(bytes.NewReader(value))

		decoder.SetMapDecoder(decodeMap)

		result, err := decoder.DecodeInterface()
		if err != nil {
			return nil, fmt.Errorf("eval: result %d: %w", index+1, err)
		}

		results = append(results, normalized(result))
	}

	return results, nil
}

// decodeMap decodes a map of any keys.
func decodeMap(decoder *msgpack.Decoder) (any, error) {
	return decoder.DecodeUntypedMap() //nolint:wrapcheck // The decoder wraps the error.
}

// normalized returns value, as the MessagePack decoder makes it, with the
// types Conn.Eval returns: every integer an int64, or a uint64 above
// math.MaxInt64, and every float a float64, in the arrays and the maps too.
func normalized(value any) any {
	switch typed := value.(type) {
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		if typed <= math.MaxInt64 {
			return int64(typed)
		}

		return typed
	case float32:
		return float64(typed)
	case []any:
		for i, item := range typed {
			typed[i] = normalized(item)
		}

		return typed
	case map[any]any:
		out := make(map[any]any, len(typed))
		for key, item := range typed {
			out[normalized(key)] = normalized(item)
		}

		return out
	default:
		return value
	}
}
