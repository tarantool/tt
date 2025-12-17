//go:build !tt_ssl_disable
// +build !tt_ssl_disable

package dial

import (
	"github.com/tarantool/go-tarantool/v2"
	"github.com/tarantool/go-tlsdialer"
)

func ssl(opts Opts) (tarantool.Dialer, error) {
	return tlsdialer.OpenSSLDialer{
		Address:         opts.Address,
		Auth:            opts.Auth,
		User:            opts.User,
		Password:        opts.Password,
		SslKeyFile:      opts.SslKeyFile,
		SslCertFile:     opts.SslCertFile,
		SslCaFile:       opts.SslCaFile,
		SslCiphers:      opts.SslCiphers,
		SslPassword:     opts.SslPassword,
		SslPasswordFile: opts.SslPasswordFile,
	}, nil
}
