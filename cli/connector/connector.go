package connector

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/FZambia/tarantool"
)

type Protocol string

type Conn struct {
	protocol Protocol

	plainText net.Conn
	binary    *tarantool.Connection

	evalFunc func(conn *Conn, funcBody string, args []interface{}, execOpts ExecOpts) ([]interface{}, error)
	callFunc func(conn *Conn, funcName string, args []interface{}, execOpts ExecOpts) ([]interface{}, error)
}

type Opts struct {
	Username string
	Password string
}

type ExecOpts struct {
	PushCallback func(interface{})
	ReadTimeout  time.Duration
	ResData      interface{}
}

const (
	// https://www.tarantool.io/en/doc/1.10/dev_guide/internals_index/#greeting-packet
	greetingSize = 1024

	PlainTextProtocol Protocol = "plain text"
	BinaryProtocol    Protocol = "binary"

	SimpleOperationTimeout = 3 * time.Second
)

func Connect(connString string, opts Opts) (*Conn, error) {
	var err error

	conn := &Conn{}

	connOpts := getConnOpts(connString, opts.Username, opts.Password)

	// connect to specified address
	plainTextConn, err := net.Dial(connOpts.Network, connOpts.Address)
	if err != nil {
		return nil, fmt.Errorf("Failed to dial: %s", err)
	}

	// detect protocol
	conn.protocol, err = getProtocol(plainTextConn)
	if err != nil {
		return nil, fmt.Errorf("Failed to get protocol: %s", err)
	}

	// initialize connection
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

func (conn *Conn) Exec(req *Request) ([]interface{}, error) {
	return req.execFunc(conn)
}

func (conn *Conn) ExecTyped(req *Request, resData interface{}) error {
	return req.execTypedFunc(conn, resData)
}

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
