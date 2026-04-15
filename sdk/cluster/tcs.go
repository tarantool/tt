package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gstorage "github.com/tarantool/go-storage/v2"
	gsconnect "github.com/tarantool/go-storage/v2/connect"
	"github.com/tarantool/go-storage/v2/driver/tcs"
	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/tt/sdk/dial"
)

const defaultStorageConnectTimeout = 5 * time.Second

// NewTCSStorage connects to the first reachable endpoint using tt's selected
// TLS backend. SSL.Enable determines the transport: false explicitly selects
// plaintext even when SSL file paths are set. Each endpoint is bounded by
// DialTimeout (5 seconds when zero), with ctx as the overall setup limit.
//
// The connection supports reads from read-only endpoints; writes return the
// server's usual read-only error. The caller must invoke cleanup when finished.
func NewTCSStorage(ctx context.Context, cfg gsconnect.Config,
) (gstorage.Storage, gsconnect.CleanupFunc, error) {
	conn, err := connectTarantoolContext(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	return gstorage.NewStorage(tcs.New(conn)), func() { _ = conn.Close() }, nil
}

func connectTarantoolContext(ctx context.Context,
	cfg gsconnect.Config,
) (tarantool.Connector, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, errFailedToConnectToTarantoolAtLeastOneEndpointIsRequired
	}

	timeout := cfg.DialTimeout
	if timeout == 0 {
		timeout = defaultStorageConnectTimeout
	}

	transport := "plain"
	if cfg.SSL.Enable {
		transport = "ssl"
	}

	var connectionErrors []error

	for _, endpoint := range cfg.Endpoints {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("failed to connect to tarantool: %w", ctx.Err())
		}

		address := strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://")

		dialer, err := dial.New(dial.Opts{
			Address:         address,
			Auth:            tarantool.AutoAuth,
			User:            cfg.Username,
			Password:        cfg.Password,
			SslKeyFile:      cfg.SSL.KeyFile,
			SslCertFile:     cfg.SSL.CertFile,
			SslCaFile:       cfg.SSL.CaFile,
			SslCiphers:      cfg.SSL.Ciphers,
			SslPassword:     cfg.SSL.Password,
			SslPasswordFile: cfg.SSL.PasswordFile,
			Transport:       transport,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create tarantool dialer: %w", err)
		}

		connectCtx, cancel := context.WithTimeout(ctx, timeout)
		conn, err := tarantool.Connect(connectCtx, dialer, tarantool.Opts{Timeout: timeout})

		cancel()

		if err == nil {
			return conn, nil
		}

		connectionErrors = append(connectionErrors, fmt.Errorf("%s: %w", endpoint, err))
	}

	return nil, fmt.Errorf("failed to connect to tarantool: %w", errors.Join(connectionErrors...))
}
