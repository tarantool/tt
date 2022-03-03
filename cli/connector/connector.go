package connector

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/FZambia/tarantool"
)

// Protocol describes the type of protocol used (plain text or IPROTO).
type Protocol string

// ExecOpts describes the parameters of the operation to be executed.
type ExecOpts struct {
	// PushCallback is the cb that will be called when a "push" message is received.
	PushCallback func(interface{})
	// ReadTimeout timeout for the operation.
	ReadTimeout time.Duration
	// ResData describes the typed result of the operation executed.
	ResData interface{}
}

// Conn describes the connection to the tarantool instance.
type Conn struct {
	protocol Protocol

	plainText net.Conn
	binary    *tarantool.Connection

	evalFunc func(conn *Conn, funcBody string, args []interface{}, execOpts ExecOpts) ([]interface{}, error)
	callFunc func(conn *Conn, funcName string, args []interface{}, execOpts ExecOpts) ([]interface{}, error)
}

const (
	// https://www.tarantool.io/en/doc/1.10/dev_guide/internals_index/#greeting-packet
	greetingSize = 1024

	PlainTextProtocol Protocol = "plain text"
	BinaryProtocol    Protocol = "binary"

	SimpleOperationTimeout = 3 * time.Second
)

// Connect connects to the tarantool instance according to "connString".
func Connect(connString string, username string, password string) (*Conn, error) {
	var err error

	conn := &Conn{}

	connOpts := GetConnOpts(connString, username, password)

	// Connect to specified address.
	plainTextConn, err := net.Dial(connOpts.Network, connOpts.Address)
	if err != nil {
		return nil, fmt.Errorf("Failed to dial: %s", err)
	}

	// Detect protocol.
	conn.protocol, err = getProtocol(plainTextConn)
	if err != nil {
		return nil, fmt.Errorf("Failed to get protocol: %s", err)
	}

	// Initialize connection.
	switch conn.protocol {
	case PlainTextProtocol:
		if err := initPlainTextConn(conn, plainTextConn); err != nil {
			return nil, err
		}
	case BinaryProtocol:
		if err := initBinaryConn(conn, connOpts); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("Unsupported protocol: %s", conn.protocol)
	}

	return conn, nil
}

// Exec executes an operation.
func (conn *Conn) Exec(req *Request) ([]interface{}, error) {
	return req.execFunc(conn)
}

// ExecTyped executes an operation and returns the typed result.
func (conn *Conn) ExecTyped(req *Request, resData interface{}) error {
	return req.execTypedFunc(conn, resData)
}

// Close closes the connection.
func (conn *Conn) Close() error {
	switch conn.protocol {
	case PlainTextProtocol:
		return conn.plainText.Close()
	case BinaryProtocol:
		return conn.binary.Close()
	default:
		return fmt.Errorf("Unsupported protocol: %s", conn.protocol)
	}
}

func getProtocol(conn net.Conn) (Protocol, error) {
	greeting, err := readGreeting(conn)
	if err != nil {
		return "", fmt.Errorf("Failed to read Tarantool greeting: %s", err)
	}

	switch {
	case strings.Contains(greeting, "(Lua console)"):
		return PlainTextProtocol, nil
	case strings.Contains(greeting, "(Binary)"):
		return BinaryProtocol, nil
	default:
		return "", fmt.Errorf("Unknown protocol: %s", greeting)
	}
}

func readGreeting(conn net.Conn) (string, error) {
	conn.SetReadDeadline(time.Now().Add(SimpleOperationTimeout))

	greeting := make([]byte, greetingSize)
	if _, err := conn.Read(greeting); err != nil {
		return "", fmt.Errorf("Failed to read Tarantool greeting: %s", err)
	}

	return string(greeting), nil
}
