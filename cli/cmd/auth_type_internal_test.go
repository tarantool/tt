package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/dial"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/connector"
)

func TestAuthTypeValue(t *testing.T) {
	auth := dial.AuthPapSha256
	value := authTypeValue{auth: &auth}

	assert.Equal(t, "pap-sha256", value.String())
	assert.Equal(t, "string", value.Type())
	assert.Equal(t, "auto", authTypeValue{}.String())

	for _, testCase := range []struct {
		name string
		want dial.Auth
	}{
		{"auto", dial.AuthAuto},
		{"chap-sha1", dial.AuthChapSha1},
		{"pap-sha256", dial.AuthPapSha256},
	} {
		require.NoError(t, value.Set(testCase.name))
		assert.Equal(t, testCase.want, auth)
		assert.Equal(t, testCase.name, value.String())
	}

	auth = dial.AuthChapSha1

	for _, name := range []string{"", "PAP-SHA256", "md5"} {
		err := value.Set(name)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "unknown authentication method")
		assert.Contains(t, err.Error(), "use auto, chap-sha1, pap-sha256")
		assert.Equal(t, dial.AuthChapSha1, auth, "a bad name keeps the value")
	}
}

// Every command that opens a binary connection to a target takes
// --auth-type, and it reaches the options of the connection.
func TestAuthTypeFlag(t *testing.T) {
	oldConnect, oldReplicaset, oldBackup := connectAuthType, replicasetAuthType, backupAuthType

	t.Cleanup(func() {
		connectAuthType, replicasetAuthType, backupAuthType = oldConnect, oldReplicaset, oldBackup
	})

	connectConnectCtx := func() connect.ConnectCtx {
		return connect.ConnectCtx{Auth: connectAuthType}
	}

	cases := []struct {
		name       string
		newCommand func() *cobra.Command
		connectCtx func() connect.ConnectCtx
	}{
		{"connect", NewConnectCmd, connectConnectCtx},
		{"replicaset status", newStatusCmd, newReplicasetConnectCtx},
		{"replicaset promote", newPromoteCmd, newReplicasetConnectCtx},
		{"cluster topology", NewClusterTopologyCmd, topologyConnectContext},
		{"backup start", newBackupStartCmd, backupConnectCtx},
		{"backup finalize", newBackupFinalizeCmd, backupConnectCtx},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// The commands of a group share the variable of the flag.
			connectAuthType, replicasetAuthType, backupAuthType =
				dial.AuthAuto, dial.AuthAuto, dial.AuthAuto

			command := testCase.newCommand()
			flag := command.Flags().Lookup("auth-type")
			require.NotNil(t, flag)
			assert.Equal(t, "auto", flag.DefValue)

			connectCtx := testCase.connectCtx()
			assert.Equal(t, dial.AuthAuto, connectCtx.Auth)

			require.NoError(t, command.Flags().Parse([]string{"--auth-type=pap-sha256"}))

			connectCtx = testCase.connectCtx()

			opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx,
				"tcp://user:pass@localhost:3301?transport=ssl")
			require.NoError(t, err)
			assert.Equal(t, dial.AuthPapSha256, opts.Auth)

			err = command.Flags().Parse([]string{"--auth-type=sha256"})
			require.ErrorContains(t, err, `invalid argument "sha256" for "--auth-type"`)
		})
	}
}

func TestDataPlayAuthTypeFlag(t *testing.T) {
	command := newDataPlayCmd()
	flag := command.Flags().Lookup("auth-type")
	require.NotNil(t, flag)
	assert.Equal(t, "auto", flag.DefValue)
	require.NoError(t, command.Flags().Parse([]string{"--auth-type", "chap-sha1"}))
	assert.Equal(t, "chap-sha1", flag.Value.String())
}

func TestApplyURIQueryAuthType(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		ctx  connect.ConnectCtx
		want dial.Auth
	}{
		{name: "no query", uri: "localhost:3301", want: dial.AuthAuto},
		{name: "auto", uri: "localhost:3301?auth_type=auto", want: dial.AuthAuto},
		{name: "chap-sha1", uri: "localhost:3301?auth_type=chap-sha1", want: dial.AuthChapSha1},
		{
			name: "pap-sha256 with ssl",
			uri:  "localhost:3301?transport=ssl&auth_type=pap-sha256",
			want: dial.AuthPapSha256,
		},
		{
			name: "a flag and no parameter",
			uri:  "localhost:3301?transport=ssl",
			ctx:  connect.ConnectCtx{Auth: dial.AuthPapSha256},
			want: dial.AuthPapSha256,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			connectCtx := testCase.ctx

			base, err := applyURIQuery(&connectCtx, testCase.uri)
			require.NoError(t, err)
			assert.Equal(t, "localhost:3301", base)
			assert.Equal(t, testCase.want, connectCtx.Auth)
		})
	}
}

func TestApplyURIQueryAuthTypeInvalid(t *testing.T) {
	cases := []struct {
		name    string
		uri     string
		ctx     connect.ConnectCtx
		wantErr error
	}{
		{name: "unknown", uri: "localhost:3301?auth_type=md5", wantErr: errUnknownAuthType},
		{name: "case", uri: "localhost:3301?auth_type=PAP-SHA256", wantErr: errUnknownAuthType},
		{name: "empty", uri: "localhost:3301?auth_type=", wantErr: errUnknownAuthType},
		{
			name:    "given by a flag",
			uri:     "localhost:3301?auth_type=chap-sha1",
			ctx:     connect.ConnectCtx{Auth: dial.AuthPapSha256},
			wantErr: errURIParamSpecifiedByFlag,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := applyURIQuery(&testCase.ctx, testCase.uri)
			require.ErrorIs(t, err, testCase.wantErr)
			assert.Contains(t, err.Error(), `"auth_type": `)
		})
	}
}

// The authentication method of the query reaches the options of the
// connection for every form of a URI, with the transport beside it.
func TestResolveConnectOptsAuthType(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		auth      dial.Auth
		transport dial.Transport
	}{
		{
			name:      "credentials",
			target:    "user:secretpw@localhost:3301?transport=ssl&auth_type=pap-sha256",
			auth:      dial.AuthPapSha256,
			transport: dial.TransportSsl,
		},
		{
			name:   "base URI",
			target: "tcp://localhost:3301?auth_type=chap-sha1",
			auth:   dial.AuthChapSha1,
		},
		{
			name:      "unix socket",
			target:    "unix/:/var/run/app.sock?transport=plain&auth_type=chap-sha1",
			auth:      dial.AuthChapSha1,
			transport: dial.TransportPlain,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			connectCtx := connect.ConnectCtx{}

			opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, testCase.target)
			require.NoError(t, err)
			assert.Equal(t, testCase.auth, opts.Auth)
			assert.Equal(t, testCase.transport, opts.Ssl.Transport)
			assert.NotContains(t, connectCtx.ConnectTarget, "auth_type")
		})
	}
}

// pap-sha256 over a plain connection is refused before anything is dialed,
// with the error of sdk/dial, whichever way the transport is plain.
func TestConnectPapSha256Plain(t *testing.T) {
	for _, target := range []string{
		"user:secretpw@127.0.0.1:1?auth_type=pap-sha256",
		"user:secretpw@127.0.0.1:1?transport=plain&auth_type=pap-sha256",
	} {
		t.Run(target, func(t *testing.T) {
			connectCtx := connect.ConnectCtx{}

			opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, target)
			require.NoError(t, err)

			conn, err := connector.Connect(opts)
			require.ErrorIs(t, err, dial.ErrPapSha256Unencrypted)
			assert.Nil(t, conn)
			assert.NotContains(t, err.Error(), "secretpw")
		})
	}
}
