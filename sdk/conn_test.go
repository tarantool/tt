package sdk_test

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
)

// TestAddConnectFlags checks the flags AddConnectFlags declares: their names,
// shorthands and defaults, the options they fill, the refusal of an unknown
// authentication method and the completion of the methods.
func TestAddConnectFlags(t *testing.T) {
	t.Parallel()

	var opts sdk.ConnectOptions

	cmd := &cobra.Command{Use: "record"}
	sdk.AddConnectFlags(cmd, &opts)

	assert.Equal(t, sdk.DefaultConnectTimeout, opts.ConnectTimeout,
		"the default of --connect-timeout is set")

	for name, want := range map[string]struct{ shorthand, def string }{
		"username":        {"u", ""},
		"password":        {"p", ""},
		"auth-type":       {"", "auto"},
		"sslkeyfile":      {"", ""},
		"sslcertfile":     {"", ""},
		"sslcafile":       {"", ""},
		"sslciphers":      {"", ""},
		"sslpassword":     {"", ""},
		"sslpasswordfile": {"", ""},
		"connect-timeout": {"", "1s"},
		sdk.ScopeFlag:     {"", ""},
		sdk.TemplateFlag:  {"", ""},
	} {
		flag := cmd.Flags().Lookup(name)
		require.NotNil(t, flag, name)
		assert.Equal(t, want.shorthand, flag.Shorthand, name)
		assert.Equal(t, want.def, flag.DefValue, name)
		assert.NotEmpty(t, flag.Usage, name)
	}

	require.NoError(t, cmd.Flags().Parse([]string{
		"-u", "user", "-p", "secret", "--auth-type", "pap-sha256",
		"--sslkeyfile", "k.pem", "--sslcertfile", "c.pem", "--sslcafile", "ca.pem",
		"--sslciphers", "ECDHE:AES256", "--sslpassword", "keypw", "--sslpasswordfile", "pw",
		"--connect-timeout", "250ms", "--scope", "user", "--template", "other",
	}))
	assert.Equal(t, sdk.ConnectOptions{
		Lookup:   sdk.AppLookup{Scope: "user", Template: "other"},
		Username: "user", Password: "secret", AuthType: "pap-sha256",
		SslKeyFile: "k.pem", SslCertFile: "c.pem", SslCaFile: "ca.pem",
		SslCiphers: "ECDHE:AES256", SslPassword: "keypw", SslPasswordFile: "pw",
		ConnectTimeout: 250 * time.Millisecond,
	}, opts)
	assert.Equal(t, "pap-sha256", cmd.Flags().Lookup("auth-type").Value.String())

	for _, name := range []string{"auto", "chap-sha1"} {
		require.NoError(t, cmd.Flags().Set("auth-type", name))
		assert.Equal(t, name, opts.AuthType)
	}

	for _, name := range []string{"", "PAP-SHA256", "md5"} {
		err := cmd.Flags().Set("auth-type", name)
		require.ErrorContains(t, err, "unknown authentication method", name)
		require.ErrorContains(t, err, "use auto, chap-sha1, pap-sha256", name)
		assert.Equal(t, "chap-sha1", opts.AuthType, "a bad name keeps the value")
	}

	complete, ok := cmd.GetFlagCompletionFunc("auth-type")
	require.True(t, ok)

	got, directive := complete(cmd, nil, "")
	assert.Equal(t, []string{"auto", "chap-sha1", "pap-sha256"}, got)
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)

	_, ok = cmd.GetFlagCompletionFunc(sdk.ScopeFlag)
	assert.True(t, ok, "--scope completes as AddAppFlags has it")
}
