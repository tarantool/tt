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
	"github.com/tarantool/tt/lib/dial"
)

var (
	errEncryptionRequired = errors.New(
		"unencrypted connection established, but encryption required",
	)
	errSocketNameIsLongerThanSymbols = errors.New("socket name is longer than ")
	errUnsupportedProtocol           = errors.New("unsupported protocol: ")
)

const (
	greetingOperationTimeout = 3 * time.Second
	maxSocketPathLinux       = 108
	maxSocketPathMac         = 106
	socketPathPrefixLength   = 3
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

// Connect connects to the tarantool instance according to options.
func Connect(opts ConnectOpts) (Connector, error) {
	// It became common that address is longer than 108 symbols(sun_path limit).
	// To reduce length of address we use relative path
	// with chdir into a directory of socket.
	// e.g foo/bar/123.sock -> ./123.sock.
	workDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	maxSocketPath := maxSocketPathLinux
	if runtime.GOOS == "darwin" {
		maxSocketPath = maxSocketPathMac
	}

	if _, err := os.Stat(opts.Address); err == nil {
		_ = os.Chdir(filepath.Dir(opts.Address))
		opts.Address = "./" + filepath.Base(opts.Address)
		if len(opts.Address)+1 > maxSocketPath {
			return nil, fmt.Errorf("%w%d symbols: %s", errSocketNameIsLongerThanSymbols,
				maxSocketPath-socketPathPrefixLength, filepath.Base(opts.Address))
		}
		defer func() {
			_ = os.Chdir(workDir)
		}()
	}
	// Connect to specified address.
	greetingConn, err := (&net.Dialer{}).DialContext(
		context.Background(), opts.Network, opts.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	// Set a deadline for the greeting.
	_ = greetingConn.SetReadDeadline(time.Now().Add(greetingOperationTimeout))

	// Detect transport and protocol.
	ssl := opts.Ssl.KeyFile != "" || opts.Ssl.CertFile != "" ||
		opts.Ssl.CaFile != "" || opts.Ssl.Ciphers != ""
	protocol, err := GetProtocol(greetingConn)
	if err != nil {
		if ssl {
			protocol = BinaryProtocol
		} else {
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

		dialer, err := dial.New(dial.Opts{
			Address:     addr,
			User:        opts.Username,
			Password:    opts.Password,
			SslKeyFile:  opts.Ssl.KeyFile,
			SslCertFile: opts.Ssl.CertFile,
			SslCaFile:   opts.Ssl.CaFile,
			SslCiphers:  opts.Ssl.Ciphers,
		})
		if err != nil {
			return nil, err
		}

		conn, err := tarantool.Connect(context.Background(), dialer, tarantool.Opts{
			SkipSchema: true, // We don't need a schema for eval requests.
		})
		if err != nil {
			return nil, err
		}
		return NewBinaryConnector(conn), nil
	default:
		return nil, fmt.Errorf("%w%s", errUnsupportedProtocol, protocol)
	}
}
