package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkconnect "github.com/tarantool/tt/sdk/connect/v3"
	"github.com/tarantool/tt/sdk/dial/v3"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/connector"
)

// iprotoProject lays out the project myapp, whose default template runs the
// Tarantool instance storage-1 with global, YAML of the global level of the
// cluster config, makes it the -C directory and returns its directory.
func iprotoProject(t *testing.T, global string) string {
	t.Helper()

	t.Setenv("TT_USER_ROOT", t.TempDir())
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	dir := filepath.Join(t.TempDir(), "myapp")

	for name, content := range map[string]string{
		".templates/default/instances.yaml": "storage-1:\n",
		".templates/default/config.yaml": global + "groups:\n  g:\n    replicasets:\n" +
			"      r:\n        instances:\n          storage-1: {}\n",
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}

	withProjectDir(t, dir)

	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	return resolved
}

// binaryConnectCtx is the context of tt connect --binary with the
// credentials of the flags user and password.
func binaryConnectCtx(user, password string) connect.ConnectCtx {
	return connect.ConnectCtx{
		Username: user, Password: password, Binary: true, Transport: dial.TransportDefault,
	}
}

// TestInstanceTargetCredentials pins that an instance of an application is
// logged in to with the credentials of the flags only, while a URI without
// credentials still takes those of TT_CLI_USERNAME and TT_CLI_PASSWORD: a
// user of the environment meant for a URI does not log in to an instance.
func TestInstanceTargetCredentials(t *testing.T) {
	iprotoProject(t, "iproto:\n  listen:\n  - uri: 127.0.0.1:3511\n")
	t.Setenv(sdkconnect.TarantoolUsernameEnv, "envuser")
	t.Setenv(sdkconnect.TarantoolPasswordEnv, "envpassword")

	cmdCtx := &cmdcontext.CmdCtx{}

	connectCtx := binaryConnectCtx("", "")
	opts, err := resolveConnectOpts(cmdCtx, &connectCtx, "myapp:storage-1")
	require.NoError(t, err)
	assert.Equal(t, connector.TCPNetwork, opts.Network)
	assert.Equal(t, "127.0.0.1:3511", opts.Address)
	assert.Empty(t, opts.Username, "the user of the environment is for a URI")
	assert.Empty(t, opts.Password)

	connectCtx = binaryConnectCtx("client", "secret")
	opts, err = resolveConnectOpts(cmdCtx, &connectCtx, "myapp:storage-1")
	require.NoError(t, err)
	assert.Equal(t, "client", opts.Username)
	assert.Equal(t, "secret", opts.Password)

	connectCtx = binaryConnectCtx("", "")
	opts, err = resolveConnectOpts(cmdCtx, &connectCtx, "127.0.0.1:3511")
	require.NoError(t, err)
	assert.Equal(t, "envuser", opts.Username, "a URI without credentials takes them")
	assert.Equal(t, "envpassword", opts.Password)

	play := &dataPlayFlags{}
	opts, instance, err := dataPlayTarget(play, "myapp:storage-1")
	require.NoError(t, err)
	assert.True(t, instance)
	assert.Empty(t, opts.Username)

	opts, instance, err = dataPlayTarget(play, "127.0.0.1:3511")
	require.NoError(t, err)
	assert.False(t, instance)
	assert.Equal(t, "envuser", opts.Username)

	setFlag(t, &backupUsername, "backuper")
	setFlag(t, &backupPassword, "backuppassword")

	backupCtx := backupConnectCtx()

	opts, err = resolveConnectOpts(cmdCtx, &backupCtx, "myapp:storage-1")
	require.NoError(t, err)
	assert.Equal(t, "backuper", opts.Username)
	assert.Equal(t, "backuppassword", opts.Password)
}

// TestInstanceTargetAddress pins that tt connect --binary, tt data play and
// the tt backup commands reach an instance at the address its config gives
// (running.ClientIprotoAddress), with the transport and the TLS files of
// the entry of iproto.listen there unless the flags give them, and at its
// binary port when the config lists unix sockets only.
func TestInstanceTargetAddress(t *testing.T) {
	ssl := "    params:\n      transport: ssl\n      ssl_key_file: localhost.key\n" +
		"      ssl_cert_file: localhost.crt\n      ssl_ca_file: ca.crt\n"

	t.Run("advertise client", func(t *testing.T) {
		iprotoProject(t, "iproto:\n  advertise:\n    client: localhost:3513\n  listen:\n"+
			"  - uri: 127.0.0.1:3512\n  - uri: 0.0.0.0:3513\n")

		connectCtx := binaryConnectCtx("", "")
		opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, "myapp:storage-1")
		require.NoError(t, err)
		assert.Equal(t, connector.TCPNetwork, opts.Network)
		assert.Equal(t, "localhost:3513", opts.Address)
		assert.Zero(t, opts.Ssl)
	})

	t.Run("advertise client with a login", func(t *testing.T) {
		iprotoProject(t, "iproto:\n  advertise:\n    client: admin:topsecret@localhost:3513\n")

		connectCtx := binaryConnectCtx("", "")
		_, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, "myapp:storage-1")
		require.ErrorContains(t, err, "iproto.advertise.client")
		require.ErrorContains(t, err, "--username and --password")
		assert.NotContains(t, err.Error(), "topsecret")
	})

	t.Run("tls of the listener", func(t *testing.T) {
		dir := iprotoProject(t, "iproto:\n  listen:\n  - uri: unix/:./x.sock\n"+
			"  - uri: '[::]:3514'\n"+ssl)

		connectCtx := binaryConnectCtx("", "")

		connectCtx.SslCaFile = "/flags/ca.crt"

		opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, "myapp:storage-1")
		require.NoError(t, err)
		assert.Equal(t, connector.TCPNetwork, opts.Network)
		assert.Equal(t, "[::1]:3514", opts.Address)
		assert.Equal(t, connector.SslOpts{
			KeyFile:   filepath.Join(dir, "localhost.key"),
			CertFile:  filepath.Join(dir, "localhost.crt"),
			CaFile:    "/flags/ca.crt",
			Transport: dial.TransportSsl,
		}, opts.Ssl, "a flag wins over the file of the listener")
	})

	t.Run("plain listener with tls flags", func(t *testing.T) {
		iprotoProject(t, "iproto:\n  listen:\n  - uri: 127.0.0.1:3515\n")

		connectCtx := binaryConnectCtx("", "")

		connectCtx.SslCaFile = "/flags/ca.crt"

		opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, "myapp:storage-1")
		require.NoError(t, err)
		assert.Equal(t, connector.SslOpts{CaFile: "/flags/ca.crt"}, opts.Ssl)
	})

	t.Run("unix sockets only", func(t *testing.T) {
		dir := iprotoProject(t, "iproto:\n  listen:\n  - uri: unix/:./x.sock\n")

		connectCtx := binaryConnectCtx("", "")
		opts, err := resolveConnectOpts(&cmdcontext.CmdCtx{}, &connectCtx, "myapp:storage-1")
		require.NoError(t, err)
		assert.Equal(t, connector.UnixNetwork, opts.Network)
		assert.Equal(t, filepath.Join(dir, "x.sock"), opts.Address)

		_, _, err = dataPlayTarget(&dataPlayFlags{}, "myapp:storage-1")
		require.ErrorContains(t, err, "the binary port of myapp:storage-1 does not exist")
	})
}

// TestGuestDeniedHint pins how tt data play says to give credentials after a
// play as guest: the environment is named for a URI only.
func TestGuestDeniedHint(t *testing.T) {
	assert.Equal(t, "tt data play connected as guest: give the credentials of a user "+
		"that may write with --username and --password", guestDeniedHint(true))
	assert.Contains(t, guestDeniedHint(false), "TT_CLI_USERNAME and TT_CLI_PASSWORD")
}
