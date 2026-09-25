package dial

import (
	"testing"

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
			expected: tarantool.NetDialer{},
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
			expected: tarantool.NetDialer{},
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
			expected: tarantool.NetDialer{},
		},
		{
			name: "transport_plain_but_key_is_set",
			opts: Opts{
				SslKeyFile: "any.key",
				Transport:  "plain",
			},
			expected: tarantool.NetDialer{},
		},
		{
			name: "transport_plain_auth_ignored",
			opts: Opts{
				Auth:      tarantool.AutoAuth,
				Transport: "plain",
			},
			expected: tarantool.NetDialer{},
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialer, err := New(tc.opts)

			require.NoError(t, err)
			require.Equal(t, tc.expected, dialer)
		})
	}
}
