package dial

import (
	"fmt"

	"github.com/tarantool/go-tarantool/v2"
)

// New creates new dialer according to the options.
func New(opts Opts) (tarantool.Dialer, error) {
	transport, err := Parse(opts.Transport)
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
		return tarantool.NetDialer{
			Address:  opts.Address,
			User:     opts.User,
			Password: opts.Password,
		}, nil
	case TransportSsl:
		return ssl(opts)
	default:
		return nil, fmt.Errorf("unsupported transport type: %s", opts.Transport)
	}
}
