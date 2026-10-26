package cmd

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/connector"
)

func TestConnectionTLSFlags(t *testing.T) {
	oldPassword, oldPasswordFile, oldTimeout := connectSslPassword,
		connectSslPasswordFile, connectTimeout
	oldRSPassword, oldRSPasswordFile, oldRSTimeout := replicasetSslPassword,
		replicasetSslPasswordFile, replicasetConnectTimeout

	t.Cleanup(func() {
		connectSslPassword, connectSslPasswordFile, connectTimeout =
			oldPassword, oldPasswordFile, oldTimeout
		replicasetSslPassword, replicasetSslPasswordFile, replicasetConnectTimeout =
			oldRSPassword, oldRSPasswordFile, oldRSTimeout
	})

	cases := []struct {
		name       string
		newCommand func() *cobra.Command
		replicaset bool
	}{
		{name: "connect", newCommand: NewConnectCmd},
		{name: "replicaset upgrade", newCommand: newUpgradeCmd, replicaset: true},
		{name: "replicaset downgrade", newCommand: newDowngradeCmd, replicaset: true},
		{name: "replicaset status", newCommand: newStatusCmd, replicaset: true},
		{name: "replicaset promote", newCommand: newPromoteCmd, replicaset: true},
		{name: "replicaset vshard bootstrap", newCommand: newBootstrapVShardCmd, replicaset: true},
		{name: "replicaset roles add", newCommand: newRolesAddCmd, replicaset: true},
		{name: "replicaset roles remove", newCommand: newRolesRemoveCmd, replicaset: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			command := testCase.newCommand()
			flag := command.Flags().Lookup("connect-timeout")
			require.NotNil(t, flag)
			assert.Equal(t, "1s", flag.DefValue)
			require.NoError(t, command.Flags().Parse([]string{
				"--sslpassword=secret", "--sslpasswordfile=/run/secrets/key-password",
				"--connect-timeout=250ms",
			}))

			connectCtx := connect.ConnectCtx{
				SslPassword:     connectSslPassword,
				SslPasswordFile: connectSslPasswordFile,
				ConnectTimeout:  connectTimeout,
			}
			if testCase.replicaset {
				connectCtx = newReplicasetConnectCtx()
			}

			// An absent local config selects URI resolution, including credentials.
			opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{},
				&connectCtx, "tcp://user:pass@localhost:3301")
			require.NoError(t, err)
			assert.Equal(t, connector.TCPNetwork, opts.Network)
			assert.Equal(t, "localhost:3301", opts.Address)
			assert.Equal(t, "user", opts.Username)
			assert.Equal(t, "pass", opts.Password)
			assert.Equal(t, "secret", opts.Ssl.Password)
			assert.Equal(t, "/run/secrets/key-password", opts.Ssl.PasswordFile)
			assert.Equal(t, 250*time.Millisecond, opts.ConnectTimeout)
		})
	}
}
