//go:build !openssl

package dial

import (
	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/go-tlsdialer/v2"
	"github.com/tarantool/go-tlsdialer/v2/backend/gostls"
)

func newTLSBackend() tlsdialer.Backend {
	return gostls.New()
}

func ssl(opts Opts) (tarantool.Dialer, error) {
	return tlsdialer.TLSDialer{
		Backend:         newTLSBackend(),
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
