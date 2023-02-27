package connector_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/tarantool/tt/cli/connector"
)

func TestMakeConnectOpts(t *testing.T) {
	cases := []struct {
		connString string
		username   string
		password   string
		ssl        SslOpts
		expected   ConnectOpts
	}{
		{"", "", "", SslOpts{},
			ConnectOpts{
				"tcp", "", "", "", SslOpts{},
			}},
		{"", "a", "b", SslOpts{"c", "d", "e", "f"},
			ConnectOpts{
				"tcp", "", "a", "b", SslOpts{"c", "d", "e", "f"},
			}},
		{"localhost:3013", "", "", SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "", "", SslOpts{},
			}},
		{"tcp://localhost:3013", "", "", SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "", "", SslOpts{},
			}},
		{"tcp:localhost", "", "", SslOpts{},
			ConnectOpts{
				"tcp", "localhost", "", "", SslOpts{},
			}},
		{"./path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "./path/to/socket", "", "", SslOpts{},
			}},
		{"/path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "", "", SslOpts{},
			}},
		{"unix:///path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "", "", SslOpts{},
			}},
		{"unix:/path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "", "", SslOpts{},
			}},
		{"unix/:/path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "", "", SslOpts{},
			}},
		{"localhost:3013", "username", "password", SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password", SslOpts{},
			}},
		{"tcp://localhost:3013", "username", "password", SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password", SslOpts{},
			}},
		{"tcp:localhost", "username", "password", SslOpts{},
			ConnectOpts{
				"tcp", "localhost", "username", "password", SslOpts{},
			}},
		{"./path/to/socket", "username", "password", SslOpts{},
			ConnectOpts{
				"unix", "./path/to/socket", "username", "password", SslOpts{},
			}},
		{"/path/to/socket", "username", "password", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"unix:///path/to/socket", "username", "password", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"unix:/path/to/socket", "username", "password", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"unix/:/path/to/socket", "username", "password", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"username:password@localhost:3013", "", "", SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password", SslOpts{},
			}},
		{"username:password@tcp://localhost:3013", "", "", SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password", SslOpts{},
			}},
		{"username:password@tcp:localhost", "", "", SslOpts{},
			ConnectOpts{
				"tcp", "localhost", "username", "password", SslOpts{},
			}},
		{"username:password@./path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "./path/to/socket", "username", "password", SslOpts{},
			}},
		{"username:password@/path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"username:password@unix:///path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"username:password@unix:/path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"username:password@unix/:/path/to/socket", "", "", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"struser:strpass@localhost:3013", "username", "password", SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password", SslOpts{},
			}},
		{"struser:strpass@tcp://localhost:3013", "username", "password",
			SslOpts{},
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password", SslOpts{},
			}},
		{"struser:strpass@tcp:localhost", "username", "password", SslOpts{},
			ConnectOpts{
				"tcp", "localhost", "username", "password", SslOpts{},
			}},
		{"struser:strpass@./path/to/socket", "username", "password", SslOpts{},
			ConnectOpts{
				"unix", "./path/to/socket", "username", "password", SslOpts{},
			}},
		{"struser:strpass@/path/to/socket", "username", "password", SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"struser:strpass@unix:///path/to/socket", "username", "password",
			SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"struser:strpass@unix:/path/to/socket", "username", "password",
			SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
		{"struser:strpass@unix/:/path/to/socket", "username", "password",
			SslOpts{},
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password", SslOpts{},
			}},
	}

	for _, c := range cases {
		caseName := c.connString + "_" + c.username + "_" + c.password
		t.Run(caseName, func(t *testing.T) {
			opts := MakeConnectOpts(c.connString,
				c.username,
				c.password,
				c.ssl)
			assert.Equal(t, c.expected, opts)
		})
	}
}
