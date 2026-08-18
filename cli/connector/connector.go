package connector

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/tarantool/go-tarantool"
)

const (
	greetingOperationTimeout = 3 * time.Second
	maxSocketPathLinux       = 108
	maxSocketPathMac         = 106
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
		return "", nil, fmt.Errorf("socket name is longer than %d symbols: %s",
			maxSocketPath-3, filepath.Base(address))
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
	// PushCallback is the cb that will be called when a "push" message is received.
	PushCallback func(interface{})
	// ReadTimeout timeout for the operation.
	ReadTimeout time.Duration
	// ResData describes the typed result of the operation executed.
	ResData interface{}
}

// Eval is an interface that wraps Eval method.
type Evaler interface {
	// Eval passes Lua expression for evaluation.
	Eval(expr string, args []interface{}, opts RequestOpts) ([]interface{}, error)
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
	greetingConn, err := net.Dial(opts.Network, opts.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %s", err)
	}

	// Set a deadline for the greeting.
	greetingConn.SetReadDeadline(time.Now().Add(greetingOperationTimeout))

	// Detect transport and protocol.
	ssl := opts.Ssl.KeyFile != "" || opts.Ssl.CertFile != "" ||
		opts.Ssl.CaFile != "" || opts.Ssl.Ciphers != ""
	transport := ""
	protocol, err := GetProtocol(greetingConn)
	if err != nil {
		if ssl {
			protocol = BinaryProtocol
			transport = "ssl"
		} else {
			greetingConn.Close()
			return nil, fmt.Errorf("failed to get protocol: %s", err)
		}
	} else if ssl {
		greetingConn.Close()
		errMsg := "unencrypted connection established, but encryption required"
		return nil, errors.New(errMsg)
	}

	// Reset the deadline. From the SetDeadline doc:
	// "A zero value for t means I/O operations will not time out."
	greetingConn.SetDeadline(time.Time{})

	// Initialize connection.
	switch protocol {
	case TextProtocol:
		return NewTextConnector(greetingConn), nil
	case BinaryProtocol:
		greetingConn.Close()

		addr := fmt.Sprintf("%s://%s", opts.Network, opts.Address)
		conn, err := tarantool.Connect(addr, tarantool.Opts{
			User:       opts.Username,
			Pass:       opts.Password,
			Transport:  transport,
			Ssl:        tarantool.SslOpts(opts.Ssl),
			SkipSchema: true, // We don't need a schema for eval requests.
		})
		if err != nil {
			return nil, err
		}
		return NewBinaryConnector(conn), nil
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}
