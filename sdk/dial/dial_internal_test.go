package dial

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/go-tlsdialer/v2"
)

func TestNew(t *testing.T) {
	cases := []struct {
		name     string
		opts     Opts
		expected tarantool.Dialer
	}{
		{
			name:     "empty",
			opts:     Opts{},
			expected: plainDialer{},
		},
		{
			name: "key_file",
			opts: Opts{
				SslCaFile: "any.key",
			},
			expected: tlsdialer.TLSDialer{
				Backend:   newTLSBackend(),
				SslCaFile: "any.key",
			},
		},
		{
			name: "ca_file",
			opts: Opts{
				SslCaFile: "any_ca.crt",
			},
			expected: tlsdialer.TLSDialer{
				Backend:   newTLSBackend(),
				SslCaFile: "any_ca.crt",
			},
		},
		{
			name: "default",
			opts: Opts{
				Transport: "",
			},
			expected: plainDialer{},
		},
		{
			name: "default_but_key_is_set",
			opts: Opts{
				SslKeyFile: "any.key",
				Transport:  "",
			},
			expected: tlsdialer.TLSDialer{
				Backend:    newTLSBackend(),
				SslKeyFile: "any.key",
			},
		},
		{
			name: "transport_ssl",
			opts: Opts{
				Transport: "ssl",
			},
			expected: tlsdialer.TLSDialer{
				Backend: newTLSBackend(),
			},
		},
		{
			name: "transport_plain",
			opts: Opts{
				Transport: "plain",
			},
			expected: plainDialer{},
		},
		{
			name: "transport_plain_but_key_is_set",
			opts: Opts{
				SslKeyFile: "any.key",
				Transport:  "plain",
			},
			expected: plainDialer{},
		},
		{
			name: "transport_plain_credentials",
			opts: Opts{
				Address:   "tcp://localhost:3301",
				User:      "user",
				Password:  "secret",
				Transport: "plain",
			},
			expected: plainDialer{
				address:  "tcp://localhost:3301",
				user:     "user",
				password: "secret",
			},
		},
		{
			name: "transport_plain_auth_auto",
			opts: Opts{
				Auth:      tarantool.AutoAuth,
				Transport: "plain",
			},
			expected: plainDialer{},
		},
		{
			name: "transport_plain_auth_chap_sha1",
			opts: Opts{
				Auth:      tarantool.ChapSha1Auth,
				Transport: "plain",
			},
			expected: plainDialer{},
		},
		{
			name: "transport_ssl_auth",
			opts: Opts{
				Auth:      tarantool.ChapSha1Auth,
				Transport: "ssl",
			},
			expected: tlsdialer.TLSDialer{
				Backend: newTLSBackend(),
				Auth:    tarantool.ChapSha1Auth,
			},
		},
		{
			name: "transport_ssl_auth_pap_sha256",
			opts: Opts{
				Auth:      tarantool.PapSha256Auth,
				Transport: "ssl",
			},
			expected: tlsdialer.TLSDialer{
				Backend: newTLSBackend(),
				Auth:    tarantool.PapSha256Auth,
			},
		},
		{
			name: "default_auth_pap_sha256_with_ssl_setting",
			opts: Opts{
				Auth:      tarantool.PapSha256Auth,
				SslCaFile: "ca.crt",
			},
			expected: tlsdialer.TLSDialer{
				Backend:   newTLSBackend(),
				Auth:      tarantool.PapSha256Auth,
				SslCaFile: "ca.crt",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialer, err := New(tc.opts)

			require.NoError(t, err)
			require.Equal(t, tc.expected, dialer)
		})
	}
}

// pap-sha256 sends the password as it is: the plain transport, chosen or
// taken for want of an SSL setting, refuses it before it dials.
func TestNew_papSha256Unencrypted(t *testing.T) {
	for _, transport := range []string{"", "plain"} {
		t.Run("transport="+transport, func(t *testing.T) {
			dialer, err := New(Opts{
				Address:   "localhost:3301",
				Auth:      tarantool.PapSha256Auth,
				User:      "user",
				Password:  "secret",
				Transport: transport,
			})
			require.ErrorIs(t, err, ErrPapSha256Unencrypted)
			assert.Nil(t, dialer)
		})
	}
}

func TestNew_unknownAuth(t *testing.T) {
	for _, transport := range []string{"plain", "ssl"} {
		t.Run(transport, func(t *testing.T) {
			dialer, err := New(Opts{Auth: tarantool.Auth(15), Transport: transport})
			require.ErrorIs(t, err, errUnknownAuth)
			assert.Nil(t, dialer)
		})
	}
}

func TestParseAuth(t *testing.T) {
	cases := []struct {
		name string
		want tarantool.Auth
	}{
		{"", tarantool.AutoAuth},
		{"auto", tarantool.AutoAuth},
		{"chap-sha1", tarantool.ChapSha1Auth},
		{"pap-sha256", tarantool.PapSha256Auth},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			auth, err := ParseAuth(testCase.name)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, auth)
		})
	}

	// The names round-trip through tarantool.Auth.String.
	for _, name := range AuthNames {
		auth, err := ParseAuth(name)
		require.NoError(t, err)
		assert.Equal(t, name, auth.String())
	}

	for _, name := range []string{"PAP-SHA256", "chap", "sha256", " auto"} {
		t.Run("bad "+name, func(t *testing.T) {
			_, err := ParseAuth(name)
			require.ErrorIs(t, err, errUnknownAuth)
			assert.ErrorContains(t, err, "use auto, chap-sha1, pap-sha256")
		})
	}
}
