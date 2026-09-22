package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
)

func TestConnectTimeoutFlag(t *testing.T) {
	previous := connectTimeout
	t.Cleanup(func() {
		connectTimeout = previous
	})

	cmd := NewConnectCmd()
	flag := cmd.Flags().Lookup("connect-timeout")
	require.NotNil(t, flag)
	require.Equal(t, "1s", flag.DefValue)
	require.NoError(t, cmd.Flags().Set("connect-timeout", "2s"))

	opts := makeConnOpts(connector.TCPNetwork, "localhost:3301", connect.ConnectCtx{
		ConnectTimeout: connectTimeout,
	})
	require.Equal(t, 2*time.Second, opts.ConnectTimeout)
}

func TestConnectSslPasswordFlags(t *testing.T) {
	previousPassword := connectSslPassword
	previousPasswordFile := connectSslPasswordFile
	t.Cleanup(func() {
		connectSslPassword = previousPassword
		connectSslPasswordFile = previousPasswordFile
	})

	cmd := NewConnectCmd()
	require.NoError(t, cmd.Flags().Set("sslpassword", "secret"))
	require.NoError(t, cmd.Flags().Set("sslpasswordfile", "/run/secrets/key-password"))
	require.Equal(t, "secret", connectSslPassword)
	require.Equal(t, "/run/secrets/key-password", connectSslPasswordFile)

	opts := makeConnOpts(connector.TCPNetwork, "localhost:3301", connect.ConnectCtx{
		SslPassword:     connectSslPassword,
		SslPasswordFile: connectSslPasswordFile,
	})
	require.Equal(t, "secret", opts.Ssl.Password)
	require.Equal(t, "/run/secrets/key-password", opts.Ssl.PasswordFile)
}
