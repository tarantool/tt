package cmd_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/cluster"
	"github.com/tarantool/tt/cli/cluster/cmd"
	"github.com/tarantool/tt/cli/connector"
)

func TestParseUriOpts(t *testing.T) {
	const defaultTimeout = 3 * time.Second

	cases := []struct {
		Url  string
		Opts cmd.UriOpts
		Err  string
	}{
		{
			Url:  "",
			Opts: cmd.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url:  "host",
			Opts: cmd.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url:  "scheme:///prefix",
			Opts: cmd.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url: "scheme://localhost",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost:3013",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost:3013",
				Host:     "localhost:3013",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://user@localhost",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://user:pass@localhost",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Password: "pass",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/prefix",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/prefix?key=anykey",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Key:      "anykey",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/prefix?name=anyname",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Instance: "anyname",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_key_file=/any/kfile",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				KeyFile:  "/any/kfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_cert_file=/any/certfile",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CertFile: "/any/certfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_ca_path=/any/capath",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaPath:   "/any/capath",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_ca_file=/any/cafile",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaFile:   "/any/cafile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?verify_peer=true&verify_host=true",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?verify_peer=false",
			Opts: cmd.UriOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipPeerVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?verify_peer=asd",
			Opts: cmd.UriOpts{},
			Err:  "invalid verify_peer, boolean expected",
		},
		{
			Url: "scheme://localhost?verify_host=false",
			Opts: cmd.UriOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipHostVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?verify_host=asd",
			Opts: cmd.UriOpts{},
			Err:  "invalid verify_host, boolean expected",
		},
		{
			Url: "scheme://localhost?timeout=5.5",
			Opts: cmd.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  time.Duration(float64(5.5) * float64(time.Second)),
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?timeout=asd",
			Opts: cmd.UriOpts{},
			Err:  "invalid timeout, float expected",
		},
		{
			Url: "scheme://user:pass@localhost:2012/prefix" +
				"?key=anykey&name=anyname" +
				"&ssl_key_file=kfile&ssl_cert_file=certfile" +
				"&ssl_ca_path=capath&ssl_ca_file=cafile" +
				"&ssl_ciphers=foo:bar:ciphers" +
				"&verify_peer=true&verify_host=false&timeout=2",
			Opts: cmd.UriOpts{
				Endpoint:       "scheme://localhost:2012",
				Host:           "localhost:2012",
				Prefix:         "/prefix",
				Key:            "anykey",
				Instance:       "anyname",
				Username:       "user",
				Password:       "pass",
				KeyFile:        "kfile",
				CertFile:       "certfile",
				CaPath:         "capath",
				CaFile:         "cafile",
				Ciphers:        "foo:bar:ciphers",
				SkipHostVerify: true,
				Timeout:        time.Duration(2 * time.Second),
			},
			Err: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Url, func(t *testing.T) {
			uri, err := url.Parse(tc.Url)
			require.NoError(t, err)

			opts, err := cmd.ParseUriOpts(uri)
			if tc.Err != "" {
				assert.ErrorContains(t, err, tc.Err)
			} else {
				assert.Equal(t, tc.Opts, opts)
			}
		})
	}
}

func TestMakeEtcdOptsFromUriOpts(t *testing.T) {
	cases := []struct {
		Name     string
		UriOpts  cmd.UriOpts
		Expected cluster.EtcdOpts
	}{
		{
			Name:     "empty",
			UriOpts:  cmd.UriOpts{},
			Expected: cluster.EtcdOpts{},
		},
		{
			Name: "ignored",
			UriOpts: cmd.UriOpts{
				Host:     "foo",
				Prefix:   "foo",
				Key:      "bar",
				Instance: "zoo",
				Ciphers:  "foo:bar:ciphers",
			},
			Expected: cluster.EtcdOpts{},
		},
		{
			Name: "skip_host_verify",
			UriOpts: cmd.UriOpts{
				SkipHostVerify: true,
			},
			Expected: cluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "skip_peer_verify",
			UriOpts: cmd.UriOpts{
				SkipPeerVerify: true,
			},
			Expected: cluster.EtcdOpts{
				SkipHostVerify: true,
			},
		},
		{
			Name: "full",
			UriOpts: cmd.UriOpts{
				Endpoint:       "foo",
				Host:           "host",
				Prefix:         "prefix",
				Key:            "key",
				Instance:       "instance",
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
			Expected: cluster.EtcdOpts{
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
	cases := []struct {
		Name     string
		UriOpts  cmd.UriOpts
		Expected connector.ConnectOpts
	}{
		{
			Name:    "empty",
			UriOpts: cmd.UriOpts{},
			Expected: connector.ConnectOpts{
				Network: connector.TCPNetwork,
			},
		},
		{
			Name: "ignored",
			UriOpts: cmd.UriOpts{
				Endpoint:       "localhost:3013",
				Prefix:         "foo",
				Key:            "bar",
				Instance:       "zoo",
				CaPath:         "ca_path",
				SkipHostVerify: true,
				SkipPeerVerify: true,
				Timeout:        673,
			},
			Expected: connector.ConnectOpts{
				Network: connector.TCPNetwork,
			},
		},
		{
			Name: "full",
			UriOpts: cmd.UriOpts{
				Endpoint:       "scheme://foo",
				Host:           "foo",
				Prefix:         "prefix",
				Key:            "key",
				Instance:       "instance",
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
			Expected: connector.ConnectOpts{
				Network:  connector.TCPNetwork,
				Address:  "foo",
				Username: "username",
				Password: "password",
				Ssl: connector.SslOpts{
					KeyFile:  "key_file",
					CertFile: "cert_file",
					CaFile:   "ca_file",
					Ciphers:  "foo:bar:ciphers",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			etcdOpts := cmd.MakeConnectOptsFromUriOpts(tc.UriOpts)

			assert.Equal(t, tc.Expected, etcdOpts)
		})
	}
}
