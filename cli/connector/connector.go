package connector

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tarantool/go-tarantool/v3"
	"github.com/tarantool/tt/sdk/dial"
)

var (
	errEncryptionRequired = errors.New(
		"unencrypted connection established, but encryption required",
	)
	errUnsupportedProtocol   = errors.New("unsupported protocol: ")
	errPlainTransportWithSsl = errors.New("SSL options are set for the plain transport")
	errUnknownTransport      = errors.New("unknown transport: ")
)

const (
	greetingOperationTimeout = 3 * time.Second
	maxSocketPathLinux       = 108
	maxSocketPathMac         = 104
)

// RequestOpts describes the parameters of a request to be executed.
type RequestOpts struct {
	// PushCallback is called when a push message is received by the text protocol.
	// The binary protocol ignores it because go-tarantool v3 does not expose pushes.
	PushCallback func(any)
	// ReadTimeout timeout for the operation.
	ReadTimeout time.Duration
	// ResData describes the typed result of the operation executed.
	ResData any
}

// Evaler is an interface that wraps Eval method.
type Evaler interface {
	// Eval passes Lua expression for evaluation.
	Eval(expr string, args []any, opts RequestOpts) ([]any, error)
}

// Connector is an interface that wraps all method required for a
// connector.
type Connector interface {
	Evaler
	Close() error
}

// unixSocketPathLimit returns the maximum socket path length for the current OS.
func unixSocketPathLimit() int {
	if runtime.GOOS == "darwin" {
		return maxSocketPathMac
	}

	return maxSocketPathLinux
}

// prepareUnixAddress shortens long Unix socket paths without changing the
// process-wide working directory. The alias must remain until dialing completes.
func prepareUnixAddress(address string) (string, func(), error) {
	address, err := filepath.Abs(address)
	if err != nil {
		return "", nil, fmt.Errorf("failed to resolve socket path: %w", err)
	}

	if len(address)+1 <= unixSocketPathLimit() {
		return address, nil, nil
	}

	tempRoot := os.TempDir()
	// TMPDIR can itself exceed sun_path, particularly on macOS.
	if len(filepath.Join(tempRoot, "tt-socket-1234567890", "socket"))+1 > unixSocketPathLimit() {
		tempRoot = "/tmp"
	}

	dir, err := os.MkdirTemp(tempRoot, "tt-socket-")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create socket alias directory: %w", err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }
	alias := filepath.Join(dir, "socket")

	err = os.Symlink(address, alias)
	if err != nil {
		cleanup()

		return "", nil, fmt.Errorf("failed to create socket alias: %w", err)
	}

	return alias, cleanup, nil
}

// Connect connects to the tarantool instance according to options.
func Connect(opts ConnectOpts) (Connector, error) {
	return ConnectContext(context.Background(), opts)
}

// ConnectContext connects according to options. ctx bounds connection setup,
// including protocol detection, authentication and binary protocol negotiation.
func ConnectContext(ctx context.Context, opts ConnectOpts) (Connector, error) {
	ssl, err := opts.Ssl.encrypted()
	if err != nil {
		return nil, err
	}

	if opts.Network == UnixNetwork {
		address, cleanup, err := prepareUnixAddress(opts.Address)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare unix socket address: %w", err)
		}

		if cleanup != nil {
			defer cleanup()
		}

		opts.Address = address
	}

	// Connect to specified address.
	greetingConn, err := (&net.Dialer{}).DialContext(
		ctx, opts.Network, opts.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	// Cancellation must interrupt greeting reads as well as dialing.
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = greetingConn.Close()

		close(cancelled)
	})

	defer func() {
		if !stop() {
			<-cancelled
		}
	}()

	// Set a deadline for the greeting.
	_ = greetingConn.SetReadDeadline(time.Now().Add(greetingOperationTimeout))

	// Detect the protocol. A TLS server sends no greeting before the TLS
	// handshake: with TLS chosen, a silent server is a binary one, and one
	// that greets is not a TLS one.
	protocol, err := GetProtocol(greetingConn)
	if ctx.Err() != nil {
		_ = greetingConn.Close()
		return nil, fmt.Errorf("failed to get protocol: %w", ctx.Err())
	}

	if err != nil {
		if ssl {
			protocol = BinaryProtocol
		} else {
			_ = greetingConn.Close()
			return nil, fmt.Errorf("failed to get protocol: %w", err)
		}
	} else if ssl {
		_ = greetingConn.Close()
		return nil, errEncryptionRequired
	}

	// Reset the deadline. From the SetDeadline doc:
	// "A zero value for t means I/O operations will not time out.".
	_ = greetingConn.SetDeadline(time.Time{})

	// Initialize connection.
	switch protocol {
	case TextProtocol:
		return NewTextConnector(greetingConn), nil
	case BinaryProtocol:
		_ = greetingConn.Close()

		addr := fmt.Sprintf("%s://%s", opts.Network, opts.Address)

		transport := dial.TransportPlain
		if ssl {
			transport = dial.TransportSsl
		}

		dialer, err := dial.New(dial.Opts{
			Address:         addr,
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

		// Keep the existing protocol-detection deadline separate. TLS servers
		// cannot send their greeting until the binary TLS handshake starts.
		binaryCtx, cancel := context.WithTimeout(ctx, getConnectTimeout(opts))
		defer cancel()

		conn, err := tarantool.Connect(binaryCtx, dialer, tarantool.Opts{
			SkipSchema: true, // We don't need a schema for eval requests.
		})
		if err != nil {
			return nil, fmt.Errorf("failed to connect: %w", err)
		}

		return NewBinaryConnector(conn), nil
	default:
		_ = greetingConn.Close()
		return nil, fmt.Errorf("%w%s", errUnsupportedProtocol, protocol)
	}
}
