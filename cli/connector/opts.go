package connector

import (
	"fmt"
	"time"

	"github.com/tarantool/tt/sdk/dial"
)

const (
	TCPNetwork  = "tcp"
	UnixNetwork = "unix"
	// DefaultConnectTimeout bounds binary connection establishment.
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
	// ConnectTimeout bounds binary connection establishment. Non-positive values
	// select DefaultConnectTimeout. It does not limit subsequent requests.
	ConnectTimeout time.Duration
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
	// Password decrypts the private SSL key file.
	Password string
	// PasswordFile contains candidate passwords for the private SSL key file.
	PasswordFile string
	// Transport chooses whether the connection is a TLS one:
	// dial.TransportSsl makes it one even with no other option set,
	// dial.TransportPlain makes it a plain one and is an error with any
	// other option set, and dial.TransportDefault makes it one if any other
	// option is set.
	Transport dial.Transport
}

// encrypted reports whether the connection the options describe is a TLS
// one.
func (opts SslOpts) encrypted() (bool, error) {
	set := opts.KeyFile != "" || opts.CertFile != "" || opts.CaFile != "" ||
		opts.Ciphers != "" || opts.Password != "" || opts.PasswordFile != ""

	switch opts.Transport {
	case dial.TransportDefault:
		return set, nil
	case dial.TransportSsl:
		return true, nil
	case dial.TransportPlain:
		if set {
			return false, errPlainTransportWithSsl
		}

		return false, nil
	default:
		return false, fmt.Errorf("%w%s", errUnknownTransport, opts.Transport)
	}
}

func getConnectTimeout(opts ConnectOpts) time.Duration {
	if opts.ConnectTimeout <= 0 {
		return DefaultConnectTimeout
	}

	return opts.ConnectTimeout
}
