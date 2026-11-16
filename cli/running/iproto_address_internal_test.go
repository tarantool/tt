package running

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
)

// tcpAddress is an address of key at host:port with no params.
func tcpAddress(key, address string) IprotoAddress {
	return IprotoAddress{Key: key, Unix: false, Address: address, Params: IprotoParams{}}
}

// TestClientIprotoAddress pins where a client on this host reaches an
// instance by its config: iproto.advertise.client, as Tarantool checks it,
// else the first TCP address of iproto.listen with a port, unix sockets
// listed or not, an address of every interface being the loopback one, with
// the params of the entry of iproto.listen at the address; and nothing for
// an iproto.listen of unix sockets only or none at all, which leave the
// binary port.
func TestClientIprotoAddress(t *testing.T) {
	const (
		listen1 = "  - uri: 127.0.0.1:3501\n"
		listen2 = "  - uri: 127.0.0.1:3502\n"
		unix    = "  - uri: unix/:./run/x.sock\n"
	)

	sslParams := "    params:\n      transport: ssl\n      ssl_key_file: localhost.key\n" +
		"      ssl_cert_file: localhost.crt\n      ssl_ca_file: /certs/ca.crt\n" +
		"      ssl_ciphers: ECDHE-RSA-AES256-GCM-SHA384\n      ssl_password: pass\n" +
		"      ssl_password_file: key.pass\n"

	// ssl are the params of sslParams for the application in dir.
	ssl := func(dir string) IprotoParams {
		return IprotoParams{
			Transport:       "ssl",
			SslKeyFile:      filepath.Join(dir, "localhost.key"),
			SslCertFile:     filepath.Join(dir, "localhost.crt"),
			SslCaFile:       "/certs/ca.crt",
			SslCiphers:      "ECDHE-RSA-AES256-GCM-SHA384",
			SslPassword:     "pass",
			SslPasswordFile: filepath.Join(dir, "key.pass"),
		}
	}

	tests := []struct {
		name   string
		env    map[string]string
		record string
		config string
		// expected returns the address for the application in dir.
		expected func(dir string) IprotoAddress
	}{
		{
			name: "advertise client over listen",
			config: "iproto:\n  advertise:\n    client: 127.0.0.1:3502\n  listen:\n" +
				listen1 + listen2,
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, "127.0.0.1:3502")
			},
		},
		{
			name:   "advertise client without listen",
			config: "iproto:\n  advertise:\n    client: db.example.com:3503\n",
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, "db.example.com:3503")
			},
		},
		{
			name:   "advertise client of a bare port",
			config: "iproto:\n  advertise:\n    client: '3503'\n",
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, "127.0.0.1:3503")
			},
		},
		{
			name:   "advertise client of IPv6",
			config: "iproto:\n  advertise:\n    client: '[::1]:3503'\n",
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, "[::1]:3503")
			},
		},
		{
			name:   "advertise client with a config variable",
			config: "iproto:\n  advertise:\n    client: '{{ instance_name }}.local:3503'\n",
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, fixtureInst+".local:3503")
			},
		},
		{
			name: "advertise client of a unix socket",
			config: "iproto:\n  advertise:\n    client: unix/:./run/client.sock\n  listen:\n" +
				listen1,
			expected: func(dir string) IprotoAddress {
				return IprotoAddress{
					Key: advertiseClientKey, Unix: true,
					Address: filepath.Join(dir, "run", "client.sock"),
				}
			},
		},
		{
			name:   "advertise client of a bare socket path",
			config: "iproto:\n  advertise:\n    client: /run/client.sock\n",
			expected: func(string) IprotoAddress {
				return IprotoAddress{
					Key: advertiseClientKey, Unix: true, Address: "/run/client.sock",
				}
			},
		},
		{
			name:   "advertise client from the env of the record",
			record: "{env: {TT_IPROTO_ADVERTISE_CLIENT: '127.0.0.1:3504'}}",
			config: "iproto:\n  advertise:\n    client: 127.0.0.1:3502\n  listen:\n" + listen1,
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, "127.0.0.1:3504")
			},
		},
		{
			name:   "advertise client from the environment",
			env:    map[string]string{"TT_IPROTO_ADVERTISE_CLIENT": "127.0.0.1:3505"},
			config: "iproto:\n  listen:\n" + listen1,
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, "127.0.0.1:3505")
			},
		},
		{
			name:   "an empty advertise client is unset",
			config: "iproto:\n  advertise:\n    client: ''\n  listen:\n" + listen1,
			expected: func(string) IprotoAddress {
				return tcpAddress(listenKey, "127.0.0.1:3501")
			},
		},
		{
			name: "advertise client takes the params of its listener",
			config: "iproto:\n  advertise:\n    client: db.example.com:3502\n  listen:\n" +
				listen1 + "  - uri: 0.0.0.0:3502\n" + sslParams,
			expected: func(dir string) IprotoAddress {
				return IprotoAddress{
					Key: advertiseClientKey, Address: "db.example.com:3502", Params: ssl(dir),
				}
			},
		},
		{
			name: "advertise client of the same host and port",
			config: "iproto:\n  advertise:\n    client: localhost:3502\n  listen:\n" +
				"  - uri: localhost:3502\n" + sslParams,
			expected: func(dir string) IprotoAddress {
				return IprotoAddress{
					Key: advertiseClientKey, Address: "localhost:3502", Params: ssl(dir),
				}
			},
		},
		{
			name: "advertise client no listener has",
			config: "iproto:\n  advertise:\n    client: localhost:3509\n  listen:\n" +
				"  - uri: localhost:3502\n" + sslParams,
			expected: func(string) IprotoAddress {
				return tcpAddress(advertiseClientKey, "localhost:3509")
			},
		},
		{
			name:     "tcp of a template",
			config:   "iproto:\n  listen:\n" + listen1,
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3501") },
		},
		{
			name:     "first of several",
			config:   "iproto:\n  listen:\n" + listen2 + listen1,
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3502") },
		},
		{
			name:     "tcp after a unix socket",
			config:   "iproto:\n  listen:\n" + unix + listen1,
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3501") },
		},
		{
			name:     "tcp before a unix socket",
			config:   "iproto:\n  listen:\n" + listen2 + unix,
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3502") },
		},
		{
			name:     "every interface",
			config:   "iproto:\n  listen:\n  - uri: 0.0.0.0:3501\n",
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3501") },
		},
		{
			name:     "every interface of IPv6",
			config:   "iproto:\n  listen:\n  - uri: '[::]:3501'\n",
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "[::1]:3501") },
		},
		{
			name:     "no host",
			config:   "iproto:\n  listen:\n  - uri: ':3501'\n",
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3501") },
		},
		{
			name:     "bare port",
			config:   "iproto:\n  listen:\n  - uri: '3501'\n",
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3501") },
		},
		{
			name:     "port 0 is skipped",
			config:   "iproto:\n  listen:\n  - uri: 127.0.0.1:0\n" + listen2,
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "127.0.0.1:3502") },
		},
		{
			name:   "ssl params",
			config: "iproto:\n  listen:\n" + listen1 + sslParams,
			expected: func(dir string) IprotoAddress {
				return IprotoAddress{Key: listenKey, Address: "127.0.0.1:3501", Params: ssl(dir)}
			},
		},
		{
			name:   "ssl transport of the query",
			config: "iproto:\n  listen:\n  - uri: localhost:3501?transport=ssl\n",
			expected: func(string) IprotoAddress {
				return IprotoAddress{
					Key: listenKey, Address: "localhost:3501",
					Params: IprotoParams{Transport: "ssl"},
				}
			},
		},
		{
			name:     "scheme",
			config:   "iproto:\n  listen:\n  - uri: tcp://localhost:3501\n",
			expected: func(string) IprotoAddress { return tcpAddress(listenKey, "localhost:3501") },
		},
		{
			name:   "config variable",
			config: "iproto:\n  listen:\n  - uri: '{{ instance_name }}.local:3501'\n",
			expected: func(string) IprotoAddress {
				return tcpAddress(listenKey, fixtureInst+".local:3501")
			},
		},
		{
			name:   "unix sockets only",
			config: "iproto:\n  listen:\n" + unix + "  - uri: /run/y.sock\n",
		},
		{
			name:   "no iproto.listen",
			config: "",
		},
		{
			name:   "iproto.listen of the env of the record",
			record: "{env: {TT_IPROTO_LISTEN: '[{\"uri\": \"127.0.0.1:3502\"}]'}}",
			config: "iproto:\n  listen:\n" + listen1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for name, value := range test.env {
				t.Setenv(name, value)
			}

			fixture := newAppFixture(t, app.ScopeProject, appFiles(test.record, test.config))
			inst := fixture.instanceCtx(t)

			address, found, err := ClientIprotoAddress(&inst)
			require.NoError(t, err)

			if test.expected == nil {
				assert.False(t, found)
				assert.Zero(t, address)

				return
			}

			assert.True(t, found)
			assert.Equal(t, test.expected(inst.AppDir), address)
		})
	}
}

// TestClientIprotoAddressRefused pins that a value of
// iproto.advertise.client Tarantool refuses, and does not start with, is an
// error that names it, its password hidden: credentials, which Tarantool
// takes for a client from elsewhere, URI parameters, an address of every
// interface, port 0, and no port.
func TestClientIprotoAddressRefused(t *testing.T) {
	tests := []struct {
		name     string
		client   string
		expected error
	}{
		{"login", "admin@127.0.0.1:3501", errAdvertiseLogin},
		{"login and password", "admin:topsecret@127.0.0.1:3501", errAdvertiseLogin},
		{"login of a unix socket", "admin@unix/:./run/x.sock", errAdvertiseLogin},
		{"parameters", "127.0.0.1:3501?transport=ssl", errAdvertiseParams},
		{"every interface", "0.0.0.0:3501", errAdvertiseEveryInterface},
		{"every interface of IPv6", "'[::]:3501'", errAdvertiseEveryInterface},
		{"port 0", "127.0.0.1:0", errAdvertiseZeroPort},
		{"no port", "localhost", errAdvertiseURI},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := "iproto:\n  advertise:\n    client: " + test.client +
				"\n  listen:\n  - uri: 127.0.0.1:3502\n"
			fixture := newAppFixture(t, app.ScopeProject, appFiles("", config))
			inst := fixture.instanceCtx(t)

			_, found, err := ClientIprotoAddress(&inst)
			require.ErrorIs(t, err, test.expected)
			require.ErrorContains(t, err, advertiseClientKey)
			assert.False(t, found)
			assert.NotContains(t, err.Error(), "topsecret")
		})
	}
}

// TestParseIprotoURI pins how a URI of iproto.listen or
// iproto.advertise.client is cut, as the URI parser of Tarantool cuts it.
func TestParseIprotoURI(t *testing.T) {
	tests := []struct {
		uri      string
		expected iprotoURI
		ok       bool
	}{
		{"localhost:3301", iprotoURI{host: "localhost", port: "3301"}, true},
		{"3301", iprotoURI{port: "3301"}, true},
		{"[::1]:3301", iprotoURI{host: "::1", port: "3301"}, true},
		{"tcp://localhost:3301", iprotoURI{host: "localhost", port: "3301"}, true},
		{"user@localhost:3301", iprotoURI{login: true, host: "localhost", port: "3301"}, true},
		{"user:pass@localhost:3301", iprotoURI{login: true, host: "localhost", port: "3301"}, true},
		{"unix/:/run/x.sock", iprotoURI{unix: true, path: "/run/x.sock"}, true},
		{"unix/:./x.sock", iprotoURI{unix: true, path: "./x.sock"}, true},
		{"user@unix/:./x.sock", iprotoURI{login: true, unix: true, path: "./x.sock"}, true},
		{"/run/a@b.sock", iprotoURI{unix: true, path: "/run/a@b.sock"}, true},
		{"./x.sock", iprotoURI{unix: true, path: "./x.sock"}, true},
		{"../x.sock", iprotoURI{unix: true, path: "../x.sock"}, true},
		{"unix://x.sock", iprotoURI{unix: true, path: "x.sock"}, true},
		{"localhost", iprotoURI{}, false},
		{"unix/:", iprotoURI{unix: true}, false},
		{"", iprotoURI{}, false},
	}

	for _, test := range tests {
		t.Run(test.uri, func(t *testing.T) {
			uri, ok := parseIprotoURI(test.uri)
			assert.Equal(t, test.ok, ok)

			uri.query = nil
			assert.Equal(t, test.expected, uri)
		})
	}

	uri, ok := parseIprotoURI("localhost:3301?transport=ssl&ssl_ca_file=ca.crt")
	require.True(t, ok)
	assert.True(t, uri.hasQuery)
	assert.Equal(t, "ssl", uri.query.Get("transport"))
	assert.Equal(t, "ca.crt", uri.query.Get("ssl_ca_file"))
}
