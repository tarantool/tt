//go:build integration

package core

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/connector"
)

// evalInstance is a Tarantool the tests of Conn.Eval start, with a console
// socket and an iproto socket: client may do anything, weak may log in and
// nothing more.
type evalInstance struct {
	console string
	iproto  string
}

// startEvalInstance starts the tarantool of PATH as an evalInstance in a
// directory of its own, short enough for a socket path, and stops it when
// the test ends. It skips the test without a tarantool.
func startEvalInstance(t *testing.T) evalInstance {
	t.Helper()

	exe, err := exec.LookPath("tarantool")
	if err != nil {
		t.Skip("tarantool is not found")
	}

	//nolint:usetesting // The path of t.TempDir() may be too long for a socket.
	dir, err := os.MkdirTemp("", "tc")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	instance := evalInstance{
		console: filepath.Join(dir, "console.sock"),
		iproto:  filepath.Join(dir, "iproto.sock"),
	}

	script := "box.cfg{listen = 'unix/:" + instance.iproto + "', log = 'tarantool.log'}\n" +
		"box.schema.user.create('client', {password = 'secret'})\n" +
		"box.schema.user.grant('client', 'super')\n" +
		"box.schema.user.create('weak', {password = 'secret'})\n" +
		"require('console').listen('unix/:" + instance.console + "')\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "init.lua"), []byte(script), 0o644))

	// Not the context of the test: the instance is stopped in a cleanup.
	process := exec.CommandContext(context.Background(), exe, "init.lua")

	process.Dir = dir
	require.NoError(t, process.Start())

	t.Cleanup(func() {
		_ = process.Process.Kill()
		_ = process.Wait()

		if t.Failed() {
			log, _ := os.ReadFile(filepath.Join(dir, "tarantool.log"))
			t.Logf("tarantool.log:\n%s", log)
		}
	})

	require.Eventually(t, func() bool {
		_, consoleErr := os.Stat(instance.console)
		_, iprotoErr := os.Stat(instance.iproto)

		return consoleErr == nil && iprotoErr == nil
	}, 30*time.Second, 50*time.Millisecond, "tarantool does not listen")

	return instance
}

// evalServices returns the Services of a module, tt configured.
func evalServices() *services {
	return readyServices(strings.NewReader(""), io.Discard, io.Discard)
}

// transport is a way to an evalInstance.
type transport struct {
	name   string
	target string
	opts   sdk.ConnectOptions
}

// transports are the console and iproto of instance.
func transports(instance evalInstance) []transport {
	return []transport{
		{name: "console", target: "unix/:" + instance.console, opts: sdk.ConnectOptions{}},
		{name: "iproto", target: "unix/:" + instance.iproto, opts: sdk.ConnectOptions{
			Username: "client", Password: "secret",
		}},
	}
}

// openConn opens a Conn over way and closes it when the test ends.
func openConn(t *testing.T, way transport) sdk.Conn {
	t.Helper()

	conn, err := evalServices().Connect(t.Context(), way.target, way.opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// everyByte is a string of every byte value, in order.
func everyByte() string {
	every := make([]byte, 256)
	for i := range every {
		every[i] = byte(i)
	}

	return string(every)
}

// TestConnEvalContract runs the same code with the same arguments over the
// console and over iproto, and both answer the same: the results with
// their types, nil among them, the bytes of a string, a mebibyte, the Lua
// errors without their position, and code with comments.
func TestConnEvalContract(t *testing.T) {
	instance := startEvalInstance(t)
	every := everyByte()
	mebibyte := strings.Repeat(every, 4096)

	cases := []struct {
		name string
		expr string
		args []any
		want []any
	}{
		{
			name: "comments",
			expr: "-- the arguments\nlocal a, b = ... -- two of them\n" +
				"--[[ a block\ncomment ]]\nreturn a .. b -- joined\n",
			args: []any{"x", "y"},
			want: []any{"xy"},
		},
		{
			name: "every byte there and back",
			expr: "local s = ... return s, #s",
			args: []any{every},
			want: []any{every, int64(256)},
		},
		{
			name: "every byte made by the instance",
			expr: "local s = {} for i = 0, 255 do s[#s + 1] = string.char(i) end " +
				"return table.concat(s)",
			want: []any{every},
		},
		{
			name: "a mebibyte",
			expr: "return string.rep(..., 4096)",
			args: []any{every},
			want: []any{mebibyte},
		},
		{name: "nil and a message", expr: "return nil, 'msg'", want: []any{nil, "msg"}},
		{name: "nil", expr: "return nil", want: []any{nil}},
		{name: "a trailing nil", expr: "return 1, nil", want: []any{int64(1), nil}},
		{name: "nothing", expr: "return", want: []any{}},
		{name: "no return", expr: "local x = 1", want: []any{}},
		{name: "box.NULL", expr: "return box.NULL", want: []any{nil}},
		{
			name: "nil arguments", expr: "return select('#', ...), ...",
			args: []any{"a", nil}, want: []any{int64(2), "a", nil},
		},
		{
			name: "numbers",
			expr: "return 1, -1, 1.5, 2^53, 2^63, 1ULL, -1LL, 18446744073709551615ULL, 1/0",
			want: []any{
				int64(1), int64(-1), 1.5, int64(1 << 53), uint64(1 << 63), int64(1), int64(-1),
				uint64(math.MaxUint64), math.Inf(1),
			},
		},
		{
			name: "tables",
			expr: "return {}, {1, 2, {a = 1, b = {true}}}, {[1] = 'a', x = 'b'}, {[5] = 'e'}, " +
				"box.tuple.new({1, 'a'})",
			want: []any{
				[]any{},
				[]any{int64(1), int64(2), map[any]any{"a": int64(1), "b": []any{true}}},
				map[any]any{int64(1): "a", "x": "b"},
				[]any{nil, nil, nil, nil, "e"},
				[]any{int64(1), "a"},
			},
		},
		{
			name: "arguments of every type",
			expr: "return ...",
			args: []any{
				int64(-3), uint64(7), 0.25, true, "s",
				[]any{int64(1), "x"}, map[string]any{"k": int64(2)},
			},
			want: []any{
				int64(-3), int64(7), 0.25, true, "s",
				[]any{int64(1), "x"}, map[any]any{"k": int64(2)},
			},
		},
	}

	for _, way := range transports(instance) {
		t.Run(way.name, func(t *testing.T) {
			conn := openConn(t, way)

			for _, testCase := range cases {
				t.Run(testCase.name, func(t *testing.T) {
					got, err := conn.Eval(t.Context(), testCase.expr, testCase.args)
					require.NoError(t, err)
					// A mebibyte does not go to the report whole.
					require.Len(t, got, len(testCase.want))

					for i := range got {
						assert.Equal(t, testCase.want[i], got[i], "result %d", i+1)
					}
				})
			}
		})
	}
}

// TestConnEvalErrors checks the errors of Eval over the console and over
// iproto: a Lua error wraps sdk.ErrEval and reads as its message alone, the
// same over both; code that does not compile says so, with its line; an
// error that is not of the code does not wrap sdk.ErrEval.
func TestConnEvalErrors(t *testing.T) {
	instance := startEvalInstance(t)

	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "error", expr: "error('x')", want: "x"},
		{name: "error at level 2", expr: "error('x', 2)", want: "x"},
		{name: "error of a function", expr: "local function f()\nerror('deep')\nend\nf()",
			want: "deep"},
		{name: "box.error", expr: "box.error(box.error.ILLEGAL_PARAMS, 'y')", want: "y"},
		{name: "error of a builtin", expr: "return ('x'):rep()",
			want: "bad argument #2 to '?' (number expected, got no value)"},
		{name: "a syntax error", expr: "local x =\nreturn +",
			want: "the code does not compile: eval:2: unexpected symbol near 'return'"},
	}

	for _, way := range transports(instance) {
		t.Run(way.name, func(t *testing.T) {
			conn := openConn(t, way)

			for _, testCase := range cases {
				t.Run(testCase.name, func(t *testing.T) {
					_, err := conn.Eval(t.Context(), testCase.expr, nil)
					require.ErrorIs(t, err, sdk.ErrEval)
					assert.EqualError(t, err, testCase.want)
				})
			}

			// The connection is in step after an error.
			got, err := conn.Eval(t.Context(), "return 'after'", nil)
			require.NoError(t, err)
			assert.Equal(t, []any{"after"}, got)
		})
	}

	t.Run("no right to eval", func(t *testing.T) {
		conn, err := evalServices().Connect(t.Context(), "unix/:"+instance.iproto,
			sdk.ConnectOptions{Username: "weak", Password: "secret"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		_, err = conn.Eval(t.Context(), "return 1", nil)
		require.ErrorContains(t, err, "denied for user 'weak'")
		require.NotErrorIs(t, err, sdk.ErrEval)
	})
}

// TestConnEvalDeadline checks that the deadline of ctx bounds an Eval over
// either transport, and that the Conn is closed after it.
func TestConnEvalDeadline(t *testing.T) {
	instance := startEvalInstance(t)

	for _, way := range transports(instance) {
		t.Run(way.name, func(t *testing.T) {
			conn := openConn(t, way)

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()

			start := time.Now()

			_, err := conn.Eval(ctx, "require('fiber').sleep(30)", nil)
			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Less(t, time.Since(start), 10*time.Second)

			_, err = conn.Eval(t.Context(), "return 1", nil)
			require.ErrorContains(t, err, "the connection is closed")
		})
	}
}

// TestConnectURICredentials checks where the credentials of a URI target
// come from, as tt connect takes them: the URI, else the options, else the
// environment, and both the URI and the options are refused.
func TestConnectURICredentials(t *testing.T) {
	instance := startEvalInstance(t)
	services := evalServices()

	whoami := func(t *testing.T, target string, opts sdk.ConnectOptions) []any {
		t.Helper()

		conn, err := services.Connect(t.Context(), target, opts)
		require.NoError(t, err)

		defer func() { _ = conn.Close() }()

		got, err := conn.Eval(t.Context(), "return box.session.user()", nil)
		require.NoError(t, err)

		return got
	}

	assert.Equal(t, []any{"client"}, whoami(t, "client:secret@"+instance.iproto,
		sdk.ConnectOptions{}))
	assert.Equal(t, []any{"client"}, whoami(t, "unix/:"+instance.iproto,
		sdk.ConnectOptions{Username: "client", Password: "secret"}))
	assert.Equal(t, []any{"admin"}, whoami(t, instance.console, sdk.ConnectOptions{}))

	t.Setenv("TT_CLI_USERNAME", "client")
	t.Setenv("TT_CLI_PASSWORD", "secret")
	assert.Equal(t, []any{"client"}, whoami(t, "unix/:"+instance.iproto, sdk.ConnectOptions{}))

	_, err := services.Connect(t.Context(), "client:secret@"+instance.iproto,
		sdk.ConnectOptions{Username: "client"})
	require.ErrorContains(t, err, "username and password are specified with flags and a URI")

	_, err = services.Connect(t.Context(), "unix/:"+instance.iproto,
		sdk.ConnectOptions{AuthType: "md5"})
	require.ErrorContains(t, err, "unknown authentication method")
}

// TestConsoleEvalBodyComments checks the fix of the text connector itself:
// a body of several lines with line comments runs over the console as it
// is written.
func TestConsoleEvalBodyComments(t *testing.T) {
	instance := startEvalInstance(t)

	conn, err := connector.ConnectContext(t.Context(), connector.ConnectOpts{
		Network: connector.UnixNetwork, Address: instance.console,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	got, err := conn.Eval("-- first\nlocal x = ... -- the argument\nreturn x -- last",
		[]any{"v"}, connector.RequestOpts{})
	require.NoError(t, err)
	assert.Equal(t, []any{"v"}, got)

	big, err := conn.Eval("return string.rep(..., 4096)", []any{everyByte()},
		connector.RequestOpts{ReadTimeout: 30 * time.Second})
	require.NoError(t, err)
	require.Len(t, big, 1)

	value, ok := big[0].(string)
	require.True(t, ok, "%T", big[0])
	assert.True(t, bytes.Equal([]byte(strings.Repeat(everyByte(), 4096)), []byte(value)),
		"a mebibyte of every byte comes back as it was")
}
