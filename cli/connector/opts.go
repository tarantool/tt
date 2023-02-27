package connector

import (
	"strings"
)

const (
	TCPNetwork  = "tcp"
	UnixNetwork = "unix"
)

// ConnectOpts describes options for a connection to a tarantool instance.
type ConnectOpts struct {
	// Network is a characteristic of a connection like "type" ("tcp" and
	// "unix" are used).
	Network string
	// Address of an instance.
	Address string
	// Username of the tarantool user.
	Username string
	// Password of the user.
	Password string
	// Ssl options for a connection.
	Ssl SslOpts
}

// SslOpts is a way to configure SSL connection.
type SslOpts struct {
	// KeyFile is a path to a private SSL key file.
	KeyFile string
	// CertFile is a path to an SSL certificate file.
	CertFile string
	// CaFile is a path to a trusted certificate authorities (CA) file.
	CaFile string
	// Ciphers is a colon-separated (:) list of SSL cipher suites the
	// connection can use.
	Ciphers string
}

// MakeConnectOpts creates a new connection options object according to the
// arguments passed. An username and a password values from the connection
// string are used only if the username and password from the arguments are
// empty.
func MakeConnectOpts(connString, username, password string,
	ssl SslOpts) ConnectOpts {
	connOpts := ConnectOpts{
		Username: username,
		Password: password,
		Ssl:      ssl,
	}

	connStringParts := strings.SplitN(connString, "@", 2)
	address := connStringParts[len(connStringParts)-1]

	if len(connStringParts) > 1 {
		authString := connStringParts[0]
		authStringParts := strings.SplitN(authString, ":", 2)

		if connOpts.Username == "" {
			connOpts.Username = authStringParts[0]
		}
		if len(authStringParts) > 1 && connOpts.Password == "" {
			connOpts.Password = authStringParts[1]
		}
	}

	addrLen := len(address)
	switch {
	case addrLen > 0 && (address[0] == '.' || address[0] == '/'):
		connOpts.Network = UnixNetwork
		connOpts.Address = address
	case addrLen >= 7 && address[0:7] == "unix://":
		connOpts.Network = UnixNetwork
		connOpts.Address = address[7:]
	case addrLen >= 5 && address[0:5] == "unix:":
		connOpts.Network = UnixNetwork
		connOpts.Address = address[5:]
	case addrLen >= 6 && address[0:6] == "unix/:":
		connOpts.Network = UnixNetwork
		connOpts.Address = address[6:]
	case addrLen >= 6 && address[0:6] == "tcp://":
		connOpts.Network = TCPNetwork
		connOpts.Address = address[6:]
	case addrLen >= 4 && address[0:4] == "tcp:":
		connOpts.Network = TCPNetwork
		connOpts.Address = address[4:]
	default:
		connOpts.Network = TCPNetwork
		connOpts.Address = address
	}

	return connOpts
}
