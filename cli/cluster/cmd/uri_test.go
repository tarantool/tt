package cmd_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/go-tlsdialer"

	"github.com/tarantool/tt/cli/cluster/cmd"
	libcluster "github.com/tarantool/tt/lib/cluster"
	"github.com/tarantool/tt/lib/connect"
)

func TestMakeEtcdOptsFromUriOpts(t *testing.T) {
	cases := []struct {
		Name     string
		UriOpts  connect.UriOpts
		Expected libcluster.EtcdOpts
	}{
		{
			Name:     "empty",
			UriOpts:  connect.UriOpts{},
			Expected: libcluster.EtcdOpts{},
		},
		{
			Name: "ignored",
			UriOpts: connect.UriOpts{
				Host:   "foo",
				Prefix: "foo",
				Params: map[string]string{
					"key":  "bar",
					"name": "zoo",
				},
				Ciphers: "foo:bar:ciphers",
			},
			Expected: libcluster.EtcdOpts{},
		},
		{
			Name: "skip_host_verify",
			UriOpts: connect.UriOpts{
				SkipHostVerify: true,
			},
			Expected: libcluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "skip_peer_verify",
			UriOpts: connect.UriOpts{
				SkipPeerVerify: true,
			},
			Expected: libcluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "full",
			UriOpts: connect.UriOpts{
				Endpoint: "foo",
				Host:     "host",
				Prefix:   "prefix",
				Params: map[string]string{
					"key":  "key",
					"name": "instance",
				},
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        2 * time.Second,
			},
			Expected: libcluster.EtcdOpts{
				Endpoints:      []string{"foo"},
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				SkipHostVerify: true,
				Timeout:        2 * time.Second,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			etcdOpts := cmd.MakeEtcdOptsFromUriOpts(tc.UriOpts)

			assert.Equal(t, tc.Expected, etcdOpts)
		})
	}
}

func TestMakeConnectOptsFromUriOpts(t *testing.T) {
	type Expected struct {
		dialer tarantool.Dialer
		opts   tarantool.Opts
	}
	cases := []struct {
		Name     string
		UriOpts  connect.UriOpts
		Expected Expected
	}{
		{
			Name:    "empty",
			UriOpts: connect.UriOpts{},
			Expected: Expected{
				tarantool.NetDialer{
					Address: "tcp://",
				},
				tarantool.Opts{},
			},
		},
		{
			Name: "ignored",
			UriOpts: connect.UriOpts{
				Endpoint: "localhost:3013",
				Prefix:   "foo",
				Params: map[string]string{
					"key":  "bar",
					"name": "zoo",
				},
				CaPath:         "ca_path",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        673,
			},
			Expected: Expected{
				tarantool.NetDialer{
					Address: "tcp://",
				},
				tarantool.Opts{
					Timeout: 673,
				},
			},
		},
		{
			Name: "full",
			UriOpts: connect.UriOpts{
				Endpoint: "scheme://foo",
				Host:     "foo",
				Prefix:   "prefix",
				Params: map[string]string{
					"key":  "key",
					"name": "instance",
				},
				Username:       "username",
				Password:       "password",
				KeyFile:        "key_file",
				CertFile:       "cert_file",
				CaPath:         "ca_path",
				CaFile:         "ca_file",
				Ciphers:        "foo:bar:ciphers",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        2 * time.Second,
			},
			Expected: Expected{
				tlsdialer.OpenSSLDialer{
					Address:     "tcp://foo",
					User:        "username",
					Password:    "password",
					SslKeyFile:  "key_file",
					SslCertFile: "cert_file",
					SslCaFile:   "ca_file",
					SslCiphers:  "foo:bar:ciphers",
				},
				tarantool.Opts{
					Timeout: 2 * time.Second,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			dialer, opts, err := cmd.MakeConnectOptsFromUriOpts(tc.UriOpts)

			assert.NoError(t, err)
			assert.Equal(t, tc.Expected.dialer, dialer)
			assert.Equal(t, tc.Expected.opts, opts)
		})
	}
}
