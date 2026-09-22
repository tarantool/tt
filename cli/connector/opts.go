package connector

import "time"

const (
	TCPNetwork            = "tcp"
	UnixNetwork           = "unix"
	DefaultConnectTimeout = time.Second
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
	// ConnectTimeout bounds establishing a binary connection.
	ConnectTimeout time.Duration
}

func getConnectTimeout(opts ConnectOpts) time.Duration {
	if opts.ConnectTimeout <= 0 {
		return DefaultConnectTimeout
	}
	return opts.ConnectTimeout
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
	// Password is a password for decrypting the private SSL key file.
	Password string
	// PasswordFile is a path to a file containing a password for decrypting the
	// private SSL key file.
	PasswordFile string
}
