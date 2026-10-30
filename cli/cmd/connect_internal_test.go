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

func TestResolveConnectOptsURIQuery(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		network string
		address string
		user    string
		pass    string
		title   string
		ssl     connector.SslOpts
	}{
		{
			name:    "tcp address",
			target:  "localhost:3301?transport=ssl&ssl_ca_file=/x",
			network: connector.TCPNetwork,
			address: "localhost:3301",
			title:   "localhost:3301",
			ssl:     connector.SslOpts{CaFile: "/x"},
		},
		{
			name:    "credentials",
			target:  "user:secretpw@127.0.0.1:3311?transport=ssl&ssl_ca_file=/x",
			network: connector.TCPNetwork,
			address: "127.0.0.1:3311",
			user:    "user",
			pass:    "secretpw",
			title:   "127.0.0.1:3311",
			ssl:     connector.SslOpts{CaFile: "/x"},
		},
		{
			name: "scheme and every setting",
			target: "tcp://[::1]:3301?ssl_key_file=k.pem&ssl_cert_file=c.pem&ssl_ca_file=ca.pem" +
				"&ssl_ciphers=ECDHE:AES256&ssl_password=keypw123&ssl_password_file=/run/pw",
			network: connector.TCPNetwork,
			address: "[::1]:3301",
			title:   "tcp://[::1]:3301",
			ssl: connector.SslOpts{
				KeyFile: "k.pem", CertFile: "c.pem", CaFile: "ca.pem",
				Ciphers: "ECDHE:AES256", Password: "keypw123", PasswordFile: "/run/pw",
			},
		},
		{
			name:    "unix socket with the slash form",
			target:  "unix/:/tmp/s.sock?transport=plain",
			network: connector.UnixNetwork,
			address: "/tmp/s.sock",
			title:   "unix/:/tmp/s.sock",
		},
		{
			name:    "no query",
			target:  "localhost:3301",
			network: connector.TCPNetwork,
			address: "localhost:3301",
			title:   "localhost:3301",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			connectCtx := connect.ConnectCtx{}

			opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, testCase.target)
			require.NoError(t, err)
			assert.Equal(t, testCase.network, opts.Network)
			assert.Equal(t, testCase.address, opts.Address)
			assert.Equal(t, testCase.user, opts.Username)
			assert.Equal(t, testCase.pass, opts.Password)
			assert.Equal(t, testCase.ssl, opts.Ssl)
			// The title of the console has neither the credentials nor the query.
			assert.Equal(t, testCase.title, connectCtx.ConnectTarget)
		})
	}
}

func TestResolveConnectOptsURIQueryInvalid(t *testing.T) {
	cases := []struct {
		name   string
		target string
		ctx    connect.ConnectCtx
		msg    string
	}{
		{
			name:   "unsupported parameter",
			target: "user:secretpw@localhost:3301?timeout=5",
			msg:    `"timeout": unsupported parameter`,
		},
		{
			name:   "unknown transport",
			target: "user:secretpw@localhost:3301?transport=tls",
			msg:    "unknown transport",
		},
		{
			name:   "malformed query",
			target: "user:secretpw@localhost:3301?ssl_password=secretpw%zz",
			msg:    "malformed URI query",
		},
		{
			name:   "setting given by a flag",
			target: "localhost:3301?ssl_ca_file=/x",
			ctx:    connect.ConnectCtx{SslCaFile: "/y"},
			msg:    `"ssl_ca_file": is specified with a flag`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &testCase.ctx, testCase.target)
			require.ErrorContains(t, err, testCase.msg)
			assert.NotContains(t, err.Error(), "secretpw")
		})
	}
}

// A target that is neither an instance nor a URI must not give its password
// to the error, wherever the lookup cut it.
func TestResolveConnectOptsDoNotLeakPassword(t *testing.T) {
	for _, target := range []string{
		"user:secretpw@host",
		"user:secretpw@host:port",
		"u/ser:secretpw@host:3301",
		"user:sec/retpw@host:3301",
		"tcp://user:secretpw@host",
		"secretpw@host:3301",
		":secretpw@host:3301",
		"user:secretpw@",
	} {
		t.Run(target, func(t *testing.T) {
			_, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connect.ConnectCtx{}, target)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secretpw")
			assert.NotContains(t, err.Error(), "retpw")
		})
	}
}

func TestResolveAppTargetCredentialsURI(t *testing.T) {
	application, instances, instName, err := resolveAppTarget(&cmdcontext.CmdCtx{},
		"user:secretpw@127.0.0.1:3311?transport=ssl", readConfig)
	assert.Nil(t, application)
	assert.Nil(t, instances)
	assert.Empty(t, instName)
	require.ErrorIs(t, err, errNotAnAppTarget)
	assert.NotContains(t, err.Error(), "secretpw")
}
