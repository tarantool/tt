package running

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
)

// TestConfigTCPListen pins the TCP address a client reaches an instance at
// by its config: the first entry of iproto.listen, with the variables of the
// config substituted and its transport, unless the config lists a unix
// socket, which is the binary port, or there is no iproto.listen, or the
// record sets TT_IPROTO_LISTEN.
func TestConfigTCPListen(t *testing.T) {
	tests := []struct {
		name     string
		record   string
		config   string
		expected IprotoListen
		found    bool
	}{
		{
			name:     "tcp of a template",
			config:   "iproto:\n  listen:\n  - uri: 127.0.0.1:3301\n",
			expected: IprotoListen{Address: "127.0.0.1:3301"},
			found:    true,
		},
		{
			name: "first of several",
			config: "iproto:\n  listen:\n  - uri: localhost:3301\n" +
				"  - uri: 127.0.0.1:3302\n",
			expected: IprotoListen{Address: "localhost:3301"},
			found:    true,
		},
		{
			name: "ssl transport",
			config: "iproto:\n  listen:\n  - uri: localhost:3301\n" +
				"    params:\n      transport: ssl\n      ssl_cert_file: c.crt\n",
			expected: IprotoListen{Address: "localhost:3301", TLS: true},
			found:    true,
		},
		{
			name:     "ssl transport of the query",
			config:   "iproto:\n  listen:\n  - uri: localhost:3301?transport=ssl\n",
			expected: IprotoListen{Address: "localhost:3301", TLS: true},
			found:    true,
		},
		{
			name:     "every interface",
			config:   "iproto:\n  listen:\n  - uri: 0.0.0.0:3301\n",
			expected: IprotoListen{Address: "127.0.0.1:3301"},
			found:    true,
		},
		{
			name:     "every interface of IPv6",
			config:   "iproto:\n  listen:\n  - uri: '[::]:3301'\n",
			expected: IprotoListen{Address: "[::1]:3301"},
			found:    true,
		},
		{
			name:     "bare port",
			config:   "iproto:\n  listen:\n  - uri: '3301'\n",
			expected: IprotoListen{Address: "localhost:3301"},
			found:    true,
		},
		{
			name:     "scheme and query",
			config:   "iproto:\n  listen:\n  - uri: tcp://localhost:3301?transport=plain\n",
			expected: IprotoListen{Address: "localhost:3301"},
			found:    true,
		},
		{
			name:     "config variable",
			config:   "iproto:\n  listen:\n  - uri: '{{ instance_name }}.local:3301'\n",
			expected: IprotoListen{Address: fixtureInst + ".local:3301"},
			found:    true,
		},
		{
			name: "unix socket after tcp",
			config: "iproto:\n  listen:\n  - uri: 127.0.0.1:3301\n" +
				"  - uri: unix/:./run/x.sock\n",
		},
		{
			name:   "bare unix path",
			config: "iproto:\n  listen:\n  - uri: /run/x.sock\n",
		},
		{
			name:   "no iproto.listen",
			config: "",
		},
		{
			name:   "record env",
			record: "{env: {TT_IPROTO_LISTEN: '127.0.0.1:3302'}}",
			config: "iproto:\n  listen:\n  - uri: 127.0.0.1:3301\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAppFixture(t, app.ScopeProject, appFiles(test.record, test.config))
			inst := fixture.instanceCtx(t)

			listen, found, err := ConfigTCPListen(&inst)
			require.NoError(t, err)
			assert.Equal(t, test.found, found)
			assert.Equal(t, test.expected, listen)
		})
	}
}
