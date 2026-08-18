package connector

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
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

// connectMutex serializes connections that depend on the process-wide working
// directory. prepareUnixAddress may temporarily change it to shorten a socket path.
var connectMutex sync.Mutex

// unixSocketPathLimit returns the maximum socket path length for the current OS.
func unixSocketPathLimit() int {
	if runtime.GOOS == "darwin" {
		return maxSocketPathMac
	}
	return maxSocketPathLinux
}

// prepareUnixAddress prepares a Unix socket address for use with Tarantool.
func prepareUnixAddress(address string) (string, func(), error) {
	maxSocketPath := unixSocketPathLimit()

	pathNeedsShortening := len(address)+1 > maxSocketPath
	if filepath.IsAbs(address) && !pathNeedsShortening {
		return address, nil, nil
	}

	shortAddress := "./" + filepath.Base(address)
	if pathNeedsShortening && len(shortAddress)+1 > maxSocketPath {
		return "", nil, fmt.Errorf("%w%d symbols: %s", errSocketNameIsLongerThanSymbols,
			maxSocketPath-socketPathPrefixLength, filepath.Base(address))
	}

	// Relative paths also depend on the process-wide working directory.
	connectMutex.Lock() // Unlock in cleanup.

	if !pathNeedsShortening {
		return address, connectMutex.Unlock, nil
	}

	workDir, err := os.Getwd()
	if err != nil {
		connectMutex.Unlock()
		return "", nil, fmt.Errorf("failed to get working directory: %w", err)
	}

	if err := os.Chdir(filepath.Dir(address)); err != nil {
		connectMutex.Unlock()
		return "", nil, fmt.Errorf("failed to change directory to socket directory: %w", err)
	}

	cleanup := func() {
		_ = os.Chdir(workDir)
		connectMutex.Unlock()
	}

	return shortAddress, cleanup, nil
}

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
	if opts.Network == "unix" {
		address, cleanup, err := prepareUnixAddress(opts.Address)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare unix socket address: %w", err)
		}

		if cleanup != nil {
			defer cleanup()
		}

		// Use the short address if it was prepared.
		opts.Address = address
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
