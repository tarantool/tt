package connector

import (
	"context"
	"fmt"

	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/tt/sdk/dial"
)

// ConnectIproto opens an IPROTO connection to the binary port opts name, as
// a go-tarantool connection for a command that sends requests of its own:
// streams, transactions, pipelined data changes. Unlike Connect it does not
// detect the protocol: the port must be a binary one. The connection is TLS
// one when the SSL options say so, as with Connect.
//
// ctx bounds the setup of the connection - the dial, the greeting and the
// authentication - and tntOpts configures it; its schema is not loaded.
func ConnectIproto(ctx context.Context, opts ConnectOpts,
	tntOpts tarantool.Opts,
) (*tarantool.Connection, error) {
	ssl, err := opts.Ssl.encrypted()
	if err != nil {
		return nil, err
	}

	address := opts.Address

	if opts.Network == UnixNetwork {
		alias, cleanup, err := prepareUnixAddress(address)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare unix socket address: %w", err)
		}

		if cleanup != nil {
			defer cleanup()
		}

		address = alias
	}

	transport := dial.TransportPlain
	if ssl {
		transport = dial.TransportSsl
	}

	dialer, err := dial.New(dial.Opts{
		Address:         fmt.Sprintf("%s://%s", opts.Network, address),
		Auth:            tarantool.AutoAuth,
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
	if err != nil {
		return nil, err
	}

	connectCtx, cancel := context.WithTimeout(ctx, getConnectTimeout(opts))
	defer cancel()

	tntOpts.SkipSchema = true

	conn, err := tarantool.Connect(connectCtx, dialer, tntOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return conn, nil
}
