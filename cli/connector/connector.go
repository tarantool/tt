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
	// plainGreetingMargin is what the plain dial after a failed TLS
	// connection waits for a greeting on top of twice the time the failure
	// took. A plain Tarantool greets on accept, so the dial reads the
	// greeting in about the time the failed handshake read it.
	plainGreetingMargin = 250 * time.Millisecond
	maxSocketPathLinux  = 108
	maxSocketPathMac    = 104
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

	// The dialer of the binary protocol refuses what it cannot dial before
	// the address is dialed at all.
	dialer, err := newDialer(opts, opts.Address, ssl)
	if err != nil {
		return nil, err
	}

	if ssl {
		return connectTLS(ctx, opts, dialer)
	}

	// Detect the protocol, the text console or the binary one, by the
	// greeting.
	greetingConn, protocol, err := dialGreeting(ctx, opts, greetingOperationTimeout)
	if err != nil {
		return nil, err
	}

	// Initialize connection.
	switch protocol {
	case TextProtocol:
		return NewTextConnector(greetingConn), nil
	case BinaryProtocol:
		_ = greetingConn.Close()

		return connectBinary(ctx, opts, dialer)
	default:
		_ = greetingConn.Close()
		return nil, fmt.Errorf("%w%s", errUnsupportedProtocol, protocol)
	}
}

// connectTLS connects over TLS without reading a greeting first: a TLS
// server sends none before the handshake, so a read of one could only wait
// out greetingOperationTimeout, which it did on every TLS connection. A
// server that is not a TLS one, a plain Tarantool that greets at once, fails
// the handshake; when a plain dial of it then reads a Tarantool greeting, the
// failure is errEncryptionRequired. The plain dial waits for the greeting
// twice as long as the failure took and plainGreetingMargin more, so a
// failure of a TLS server, which does not greet, or one before any dial, a
// key that does not decrypt, is not held up for long. Any other failure,
// and a failure the server answered or that ran out of time, is returned as
// it is.
func connectTLS(ctx context.Context, opts ConnectOpts, dialer tarantool.Dialer) (Connector, error) {
	start := time.Now()

	conn, err := connectBinary(ctx, opts, dialer)
	if err == nil || ctx.Err() != nil || !mayBePlainServer(err) {
		return conn, err
	}

	wait := min(2*time.Since(start)+plainGreetingMargin, greetingOperationTimeout)

	probe, _, probeErr := dialGreeting(ctx, opts, wait)
	if probeErr != nil {
		return nil, err
	}

	_ = probe.Close()

	return nil, errEncryptionRequired
}

// mayBePlainServer reports whether a failed TLS connection may have failed on
// a server that is not a TLS one: not on a timeout, which a plain Tarantool,
// greeting at once, does not let the handshake run into, nor on an error the
// server returned, which it returns only after the handshake. A TLS engine
// times the handshake out by the deadline of the connection as well as by
// the context, so a timeout is either.
func mayBePlainServer(err error) bool {
	var (
		netErr    net.Error
		serverErr tarantool.ServerError
		clientErr tarantool.ClientError
	)

	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}

	return !errors.As(err, &serverErr) && !errors.As(err, &clientErr)
}

// connectBinary connects with the binary protocol, within ConnectTimeout of
// opts, and ctx.
func connectBinary(ctx context.Context, opts ConnectOpts, dialer tarantool.Dialer) (
	Connector, error,
) {
	binaryCtx, cancel := context.WithTimeout(ctx, getConnectTimeout(opts))
	defer cancel()

	conn, err := tarantool.Connect(binaryCtx, dialer, tarantool.Opts{
		SkipSchema: true, // We don't need a schema for eval requests.
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return NewBinaryConnector(conn), nil
}

// dialGreeting dials the address of opts without TLS and reads the greeting
// a Tarantool sends first, for wait at most, and returns the connection and
// the protocol the greeting names. ctx interrupts the dial and the read.
func dialGreeting(ctx context.Context, opts ConnectOpts, wait time.Duration) (
	net.Conn, Protocol, error,
) {
	conn, err := (&net.Dialer{}).DialContext(ctx, opts.Network, opts.Address)
	if err != nil {
		return nil, BinaryProtocol, fmt.Errorf("failed to dial: %w", err)
	}

	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()

		close(cancelled)
	})

	defer func() {
		if !stop() {
			<-cancelled
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(wait))

	protocol, err := GetProtocol(conn)
	if ctx.Err() != nil {
		_ = conn.Close()
		return nil, BinaryProtocol, fmt.Errorf("failed to get protocol: %w", ctx.Err())
	}

	if err != nil {
		_ = conn.Close()
		return nil, BinaryProtocol, fmt.Errorf("failed to get protocol: %w", err)
	}

	// Reset the deadline. From the SetDeadline doc:
	// "A zero value for t means I/O operations will not time out.".
	_ = conn.SetDeadline(time.Time{})

	return conn, protocol, nil
}
