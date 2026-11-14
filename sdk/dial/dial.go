package dial

import (
	"errors"
	"fmt"

	"github.com/tarantool/go-tarantool/v3"
)

var (
	errUnsupportedTransportType = errors.New("unsupported transport type: ")
)

// New creates new dialer according to the options.
func New(opts Opts) (tarantool.Dialer, error) {
	transport, err := Parse(opts.Transport)
	if err != nil {
		return nil, err
	}

	err = checkAuth(opts.Auth)
	if err != nil {
		return nil, err
	}

	if transport == TransportDefault {
		if opts.SslKeyFile != "" || opts.SslCaFile != "" || opts.SslCertFile != "" ||
			opts.SslCiphers != "" || opts.SslPassword != "" || opts.SslPasswordFile != "" {
			transport = TransportSsl
		} else {
			transport = TransportPlain
		}
	}

	switch transport {
	case TransportPlain:
		if opts.Auth == AuthPapSha256 {
			return nil, ErrPapSha256Unencrypted
		}

		return plainDialer{
			address:  opts.Address,
			user:     opts.User,
			password: opts.Password,
		}, nil
	case TransportSsl:
		return ssl(opts)
	default:
		return nil, fmt.Errorf("%w%s", errUnsupportedTransportType, opts.Transport)
	}
}
