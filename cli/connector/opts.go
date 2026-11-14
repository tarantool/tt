package connector

import (
	"fmt"
	"time"

	"github.com/tarantool/go-tarantool/v3"
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
	// Auth is the authentication method of a binary connection:
	// dial.AuthAuto takes the server's auth_type over TLS and chap-sha1 over
	// a plain connection, which refuses dial.AuthPapSha256 before it dials.
	// A text connection does not authenticate.
	Auth dial.Auth
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

// newDialer returns the dialer of a binary connection to address, a TLS one
// when ssl is set. A combination of the options it cannot dial, such as
// pap-sha256 over a plain connection, is an error before anything is
// dialed.
func newDialer(opts ConnectOpts, address string, ssl bool) (tarantool.Dialer, error) {
	transport := dial.TransportPlain
	if ssl {
		transport = dial.TransportSsl
	}

	return dial.New(dial.Opts{
		Address:         fmt.Sprintf("%s://%s", opts.Network, address),
		Auth:            opts.Auth,
		User:            opts.Username,
		Password:        opts.Password,
		SslKeyFile:      opts.Ssl.KeyFile,
		SslCertFile:     opts.Ssl.CertFile,
		SslCaFile:       opts.Ssl.CaFile,
		SslCiphers:      opts.Ssl.Ciphers,
		SslPassword:     opts.Ssl.Password,
		SslPasswordFile: opts.Ssl.PasswordFile,
		Transport:       transport.String(),
	})
}

func getConnectTimeout(opts ConnectOpts) time.Duration {
	if opts.ConnectTimeout <= 0 {
		return DefaultConnectTimeout
	}

	return opts.ConnectTimeout
}
