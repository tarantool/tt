package connector

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tarantool/go-tarantool"
)

const (
	greetingOperationTimeout = 3 * time.Second
	maxSocketPathLinux       = 108
	maxSocketPathMac         = 106
)

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
	// It became common that address is longer than 108 symbols(sun_path limit).
	// To reduce length of address we use relative path
	// with chdir into a directory of socket.
	// e.g foo/bar/123.sock -> ./123.sock
	workDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	maxSocketPath := maxSocketPathLinux
	if runtime.GOOS == "darwin" {
		maxSocketPath = maxSocketPathMac
	}

	if _, err := os.Stat(opts.Address); err == nil {
		os.Chdir(filepath.Dir(opts.Address))
		opts.Address = "./" + filepath.Base(opts.Address)
		if len(opts.Address)+1 > maxSocketPath {
			return nil, fmt.Errorf("socket name is longer than %d symbols: %s",
				maxSocketPath-3, filepath.Base(opts.Address))
		}
		defer os.Chdir(workDir)
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
