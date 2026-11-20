package cmd

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/v3"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/exitcode"
)

// TestNotRunningErr pins the error of tt connect to an instance that is not
// running: one line that says how to start it, which exits 1.
func TestNotRunningErr(t *testing.T) {
	externalProject(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	connOpts, err := resolveConnectOpts(cmdCtx, &connect.ConnectCtx{}, "myapp:storage-1")
	require.NoError(t, err)
	require.Equal(t, connector.UnixNetwork, connOpts.Network)

	hint := "; start it with tt instance start myapp:storage-1"

	t.Run("no socket", func(t *testing.T) {
		_, dialErr := connector.Connect(connOpts)
		require.Error(t, dialErr)

		err := notRunningErr(cmdCtx, "myapp:storage-1", connOpts, dialErr)
		require.EqualError(t, err, "instance myapp:storage-1 is not running "+
			"(no control socket at "+connOpts.Address+")"+hint)
		assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	})

	t.Run("stale socket", func(t *testing.T) {
		// The socket of an instance that died: the path of the control
		// socket may be too long to listen on, so it points to a short one.
		//nolint:usetesting // The path of t.TempDir() may be too long for a socket.
		dir, err := os.MkdirTemp("", "tt")
		require.NoError(t, err)
		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		listener, err := net.ListenUnix("unix", &net.UnixAddr{
			Name: filepath.Join(dir, "s"), Net: "unix",
		})
		require.NoError(t, err)
		listener.SetUnlinkOnClose(false)
		require.NoError(t, listener.Close())

		require.NoError(t, os.MkdirAll(filepath.Dir(connOpts.Address), 0o755))
		require.NoError(t, os.Symlink(filepath.Join(dir, "s"), connOpts.Address))
		t.Cleanup(func() { _ = os.Remove(connOpts.Address) })

		_, dialErr := connector.Connect(connOpts)
		require.Error(t, dialErr)

		err = notRunningErr(cmdCtx, "myapp:storage-1", connOpts, dialErr)
		require.EqualError(t, err, "instance myapp:storage-1 is not running "+
			"(its control socket at "+connOpts.Address+" refuses connections)"+hint)
		assert.Equal(t, sdk.ExitFailure, exitcode.Code(err))
	})

	t.Run("a port nothing listens on", func(t *testing.T) {
		tcpOpts := connOpts

		tcpOpts.Network = connector.TCPNetwork
		tcpOpts.Address = "127.0.0.1:1"

		_, dialErr := connector.Connect(tcpOpts)
		require.Error(t, dialErr)

		err := notRunningErr(cmdCtx, "myapp:storage-1", tcpOpts, dialErr)
		require.EqualError(t, err, "instance myapp:storage-1 is not running "+
			"(nothing listens on 127.0.0.1:1)"+hint)
	})

	t.Run("a URI", func(t *testing.T) {
		uriOpts := connOpts

		uriOpts.Network = connector.TCPNetwork
		uriOpts.Address = "127.0.0.1:1"

		_, dialErr := connector.Connect(uriOpts)
		require.Error(t, dialErr)

		err := notRunningErr(cmdCtx, "127.0.0.1:1", uriOpts, dialErr)
		require.Equal(t, dialErr, err)
		assert.Equal(t, sdk.ExitSystem, exitcode.Code(err))
	})

	t.Run("not a dial error", func(t *testing.T) {
		other := errors.New("failed to execute command")
		require.Equal(t, other, notRunningErr(cmdCtx, "myapp:storage-1", connOpts, other))
	})
}
