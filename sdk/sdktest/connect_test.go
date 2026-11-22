package sdktest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// TestServicesConnect checks the Conns of the fake: an instance answers
// with its EvalFunc, a target without one is not found, an error of a
// target is returned, and every call is recorded.
func TestServicesConnect(t *testing.T) {
	t.Parallel()

	refused := errors.New("instance app:down is not running")

	services := sdktest.New(t,
		sdktest.WithInstance("app:storage-1",
			func(_ context.Context, expr string, args []any) ([]any, error) {
				if expr == "error('x')" {
					return nil, sdktest.EvalError("x")
				}

				return append([]any{expr}, args...), nil
			}),
		sdktest.WithConnectError("app:down", refused))
	services.Start()

	opts := sdk.ConnectOptions{Lookup: sdk.AppLookup{Scope: "user"}}

	conn, err := services.Connect(t.Context(), "app:storage-1", opts)
	require.NoError(t, err)

	got, err := conn.Eval(t.Context(), "return ...", []any{"a", int64(1)})
	require.NoError(t, err)
	assert.Equal(t, []any{"return ...", "a", int64(1)}, got)

	_, err = conn.Eval(t.Context(), "error('x')", nil)
	require.ErrorIs(t, err, sdk.ErrEval)
	require.EqualError(t, err, "x")

	opened, open := services.Conns()
	assert.Equal(t, 1, opened)
	assert.Equal(t, 1, open)

	require.NoError(t, conn.Close())
	require.NoError(t, conn.Close(), "Close of a closed Conn does nothing")

	_, err = conn.Eval(t.Context(), "return 1", nil)
	require.ErrorContains(t, err, "the connection is closed")

	_, err = services.Connect(t.Context(), "app:storage-2", sdk.ConnectOptions{})
	require.ErrorIs(t, err, sdk.ErrNotFound)

	_, err = services.Connect(t.Context(), "app:down", sdk.ConnectOptions{})
	require.ErrorIs(t, err, refused)

	opened, open = services.Conns()
	assert.Equal(t, 1, opened)
	assert.Equal(t, 0, open)

	assert.Equal(t, []sdktest.ConnectCall{
		{Target: "app:storage-1", Options: opts},
		{Target: "app:storage-2", Options: sdk.ConnectOptions{}},
		{Target: "app:down", Options: sdk.ConnectOptions{}},
	}, services.Connects())
	assert.Equal(t, []sdktest.EvalCall{
		{Target: "app:storage-1", Expr: "return ...", Args: []any{"a", int64(1)}},
		{Target: "app:storage-1", Expr: "error('x')", Args: nil},
		{Target: "app:storage-1", Expr: "return 1", Args: nil},
	}, services.Evals())
}

// TestServicesConnectContext checks that an Eval ctx ends fails with the
// error of ctx and closes the Conn, as the core's does, and that Close
// while an Eval runs does not wait for it.
func TestServicesConnectContext(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithInstance("tcp://localhost:3301",
		func(ctx context.Context, _ string, _ []any) ([]any, error) {
			<-ctx.Done()

			return nil, errors.New("no answer")
		}))
	services.Start()

	conn, err := services.Connect(t.Context(), "tcp://localhost:3301", sdk.ConnectOptions{})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()

	_, err = conn.Eval(ctx, "require('fiber').sleep(10)", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	_, open := services.Conns()
	assert.Equal(t, 0, open, "an Eval ctx ended closes the Conn")

	_, err = conn.Eval(t.Context(), "return 1", nil)
	require.ErrorContains(t, err, "the connection is closed")

	conn, err = services.Connect(t.Context(), "tcp://localhost:3301", sdk.ConnectOptions{})
	require.NoError(t, err)

	long, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		_, err := conn.Eval(long, "require('fiber').sleep(10)", nil)
		done <- err
	}()

	require.Eventually(t, func() bool { return len(services.Evals()) == 3 },
		5*time.Second, time.Millisecond)
	require.NoError(t, conn.Close())

	_, open = services.Conns()
	assert.Equal(t, 0, open)

	stop()
	require.ErrorIs(t, <-done, context.Canceled)
}
