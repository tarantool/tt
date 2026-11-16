package rocks

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goLuarocksError wraps err as go-luarocks wraps the failure of a server.
func goLuarocksError(server string, err error) error {
	return fmt.Errorf("query \"checks\" across servers: remote.HTTPRemoteIndex: load %s: %w",
		server, err)
}

func TestServerError(t *testing.T) {
	t.Parallel()

	dnsErr := &url.Error{
		Op: "Get", URL: "http://no-such-host.invalid/tt-rocks-index.json",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{
			Err: "no such host", Name: "no-such-host.invalid", IsNotFound: true,
		}},
	}
	refused := &url.Error{
		Op: "Get", URL: "http://127.0.0.1:1/manifest-5.1",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{
			Syscall: "connect", Err: syscall.ECONNREFUSED,
		}},
	}
	missing := &fs.PathError{Op: "stat", Path: "/nonexistent/dir", Err: fs.ErrNotExist}

	for _, test := range []struct {
		err  error
		want string
	}{
		{
			err: goLuarocksError("http://no-such-host.invalid/", dnsErr),
			want: `resolving "checks": cannot reach rock server http://no-such-host.invalid/: ` +
				"no such host",
		},
		{
			err: goLuarocksError("http://127.0.0.1:1/", refused),
			want: `resolving "checks": cannot reach rock server http://127.0.0.1:1/: ` +
				"connection refused",
		},
		{
			err: fmt.Errorf("remote.FileRemoteIndex: load /nonexistent/dir: %w", missing),
			want: `resolving "checks": cannot reach rock server /nonexistent/dir: ` +
				"no such file or directory",
		},
	} {
		err := serverError(`resolving "checks"`, `rocks: resolve "checks"`, test.err)

		var serverErr *ServerError

		require.ErrorAs(t, err, &serverErr)
		assert.Equal(t, test.want, err.Error())
		require.ErrorIs(t, err, test.err, "the chain is kept")

		serverErr.Origin = "TT_REGISTRIES"

		assert.Contains(t, err.Error(), " (from TT_REGISTRIES): ")

		// A ServerError further down is not made again.
		again := serverError(`resolving "x"`, "resolving", err)
		assert.Equal(t, "resolving: "+err.Error(), again.Error())
	}

	other := errors.New("no version matches")
	assert.Equal(t, `rocks: resolve "checks": no version matches`,
		serverError(`resolving "checks"`, `rocks: resolve "checks"`, other).Error())

	same, ok := AsServerError("installing checks 3.4.1-1", other)
	assert.False(t, ok)
	assert.Equal(t, other, same)

	installErr, ok := AsServerError("installing checks 3.4.1-1", refused)
	assert.True(t, ok)
	assert.Equal(t, "installing checks 3.4.1-1: cannot reach rock server http://127.0.0.1:1/: "+
		"connection refused", installErr.Error())
}

func TestServerOf(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "http://a.example/rocks/", serverOf("http://a.example/rocks/manifest-5.1"))
	assert.Equal(t, "http://a.example/", serverOf("http://a.example/tt-rocks-index.json"))
	assert.Equal(t, "http://a.example/x.rock", serverOf("http://a.example/x.rock"))
}
