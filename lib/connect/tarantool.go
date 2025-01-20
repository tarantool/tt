package connect

import (
	"fmt"
	"os"

	"github.com/tarantool/go-tarantool"
)

// TarantoolFunc is a function that can be called on a `Tarantool` connection.
type TarantoolFunc func(tarantool.Connector) error

var (
	// tarantoolConnect used for mocking purposes.
	tarantoolConnect = tarantool.Connect
)

// makeConnectOptsFromUriOpts create Tarantool connect options from
// URI options.
func makeConnectOptsFromUriOpts(src UriOpts) (string, tarantool.Opts) {
	opts := tarantool.Opts{
		User: src.Username,
		Pass: src.Password,
		Ssl: tarantool.SslOpts{
			KeyFile:  src.KeyFile,
			CertFile: src.CertFile,
			CaFile:   src.CaFile,
			Ciphers:  src.Ciphers,
		},
		Timeout: src.Timeout,
	}

	if opts.Ssl != (tarantool.SslOpts{}) {
		opts.Transport = "ssl"
	}

	return fmt.Sprintf("tcp://%s", src.Host), opts
}

// connectTarantool establishes a connection to Tarantool.
func connectTarantool(uriOpts UriOpts) (tarantool.Connector, error) {
	addr, connectorOpts := makeConnectOptsFromUriOpts(uriOpts)
	if connectorOpts.User == "" && connectorOpts.Pass == "" {
		if connectorOpts.User == "" {
			connectorOpts.User = os.Getenv(TarantoolUsernameEnv)
		}
		if connectorOpts.Pass == "" {
			connectorOpts.Pass = os.Getenv(TarantoolPasswordEnv)
		}
	}

	conn, err := tarantoolConnect(addr, connectorOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to tarantool: %w", err)
	}
	return conn, nil
}

// RunOnTarantool runs the provided function with Tarantool connection.
// Returns true if the function was executed.
func RunOnTarantool(opts UriOpts, f TarantoolFunc) (bool, error) {
	if f != nil {
		conn, err := connectTarantool(opts)
		if err != nil {
			return false, err
		}
		return true, f(conn)
	}
	return false, nil
}
