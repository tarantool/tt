package connector

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
