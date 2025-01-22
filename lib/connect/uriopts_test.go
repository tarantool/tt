package connect_test

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/lib/connect"
)

func TestParseUriOpts(t *testing.T) {
	const defaultTimeout = 3 * time.Second

	cases := []struct {
		Url  string
		User string
		Pwd  string
		Opts connect.UriOpts
		Err  string
	}{
		{
			Url:  "",
			Opts: connect.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url:  "host",
			Opts: connect.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url:  "scheme:///prefix",
			Opts: connect.UriOpts{},
			Err:  "URL must contain the scheme and the host parts",
		},
		{
			Url: "scheme://localhost",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost:3013",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost:3013",
				Host:     "localhost:3013",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://user@localhost",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://user:pass@localhost",
			Opts: connect.UriOpts{
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
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/prefix",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Prefix:   "/prefix",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost/prefix?key=anykey",
			Opts: connect.UriOpts{
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
			Opts: connect.UriOpts{
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
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				KeyFile:  "/any/kfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_cert_file=/any/certfile",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CertFile: "/any/certfile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_ca_path=/any/capath",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaPath:   "/any/capath",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?ssl_ca_file=/any/cafile",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				CaFile:   "/any/cafile",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?verify_peer=true&verify_host=true",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url: "scheme://localhost?verify_peer=false",
			Opts: connect.UriOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipPeerVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?verify_peer=asd",
			Opts: connect.UriOpts{},
			Err:  "invalid verify_peer, boolean expected",
		},
		{
			Url: "scheme://localhost?verify_host=false",
			Opts: connect.UriOpts{
				Endpoint:       "scheme://localhost",
				Host:           "localhost",
				SkipHostVerify: true,
				Timeout:        defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?verify_host=asd",
			Opts: connect.UriOpts{},
			Err:  "invalid verify_host, boolean expected",
		},
		{
			Url: "scheme://localhost?timeout=5.5",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Timeout:  time.Duration(float64(5.5) * float64(time.Second)),
			},
			Err: "",
		},
		{
			Url:  "scheme://localhost?timeout=asd",
			Opts: connect.UriOpts{},
			Err:  "invalid timeout, float expected",
		},
		{
			Url: "scheme://user:pass@localhost:2012/prefix" +
				"?key=anykey&name=anyname" +
				"&ssl_key_file=kfile&ssl_cert_file=certfile" +
				"&ssl_ca_path=capath&ssl_ca_file=cafile" +
				"&ssl_ciphers=foo:bar:ciphers" +
				"&verify_peer=true&verify_host=false&timeout=2",
			Opts: connect.UriOpts{
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
		{
			Url:  "scheme://localhost",
			User: "user",
			Pwd:  "pass",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Password: "pass",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://user:pass@localhost",
			User: "ignored_user",
			Pwd:  "ignored_pass",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Password: "pass",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
		{
			Url:  "scheme://user@localhost",
			User: "ignored_user",
			Pwd:  "ignored_pass",
			Opts: connect.UriOpts{
				Endpoint: "scheme://localhost",
				Host:     "localhost",
				Username: "user",
				Password: "",
				Timeout:  defaultTimeout,
			},
			Err: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Url, func(t *testing.T) {
			uri, err := url.Parse(tc.Url)
			require.NoError(t, err)

			opts, err := connect.ParseUriOpts(uri, tc.User, tc.Pwd)
			if tc.Err != "" {
				assert.ErrorContains(t, err, tc.Err)
			} else {
				assert.Equal(t, tc.Opts, opts)
			}
		})
	}
}
