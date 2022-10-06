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
		expected   ConnectOpts
	}{
		{"", "", "",
			ConnectOpts{
				"tcp", "", "", "",
			}},
		{"localhost:3013", "", "",
			ConnectOpts{
				"tcp", "localhost:3013", "", "",
			}},
		{"tcp://localhost:3013", "", "",
			ConnectOpts{
				"tcp", "localhost:3013", "", "",
			}},
		{"tcp:localhost", "", "",
			ConnectOpts{
				"tcp", "localhost", "", "",
			}},
		{"./path/to/socket", "", "",
			ConnectOpts{
				"unix", "./path/to/socket", "", "",
			}},
		{"/path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "", "",
			}},
		{"unix:///path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "", "",
			}},
		{"unix:/path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "", "",
			}},
		{"unix/:/path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "", "",
			}},
		{"localhost:3013", "username", "password",
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password",
			}},
		{"tcp://localhost:3013", "username", "password",
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password",
			}},
		{"tcp:localhost", "username", "password",
			ConnectOpts{
				"tcp", "localhost", "username", "password",
			}},
		{"./path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "./path/to/socket", "username", "password",
			}},
		{"/path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"unix:///path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"unix:/path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"unix/:/path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"username:password@localhost:3013", "", "",
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password",
			}},
		{"username:password@tcp://localhost:3013", "", "",
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password",
			}},
		{"username:password@tcp:localhost", "", "",
			ConnectOpts{
				"tcp", "localhost", "username", "password",
			}},
		{"username:password@./path/to/socket", "", "",
			ConnectOpts{
				"unix", "./path/to/socket", "username", "password",
			}},
		{"username:password@/path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"username:password@unix:///path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"username:password@unix:/path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"username:password@unix/:/path/to/socket", "", "",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"struser:strpass@localhost:3013", "username", "password",
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password",
			}},
		{"struser:strpass@tcp://localhost:3013", "username", "password",
			ConnectOpts{
				"tcp", "localhost:3013", "username", "password",
			}},
		{"struser:strpass@tcp:localhost", "username", "password",
			ConnectOpts{
				"tcp", "localhost", "username", "password",
			}},
		{"struser:strpass@./path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "./path/to/socket", "username", "password",
			}},
		{"struser:strpass@/path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"struser:strpass@unix:///path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"struser:strpass@unix:/path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
		{"struser:strpass@unix/:/path/to/socket", "username", "password",
			ConnectOpts{
				"unix", "/path/to/socket", "username", "password",
			}},
	}

	for _, c := range cases {
		caseName := c.connString + "_" + c.username + "_" + c.password
		t.Run(caseName, func(t *testing.T) {
			opts := MakeConnectOpts(c.connString, c.username, c.password)
			assert.Equal(t, c.expected, opts)
		})
	}
}
