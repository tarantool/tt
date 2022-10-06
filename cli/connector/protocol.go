package connector

import (
	"fmt"
	"io"
	"strings"
)

const (
	// https://www.tarantool.io/en/doc/1.10/dev_guide/internals_index/#greeting-packet
	greetingSize = 128
)

const (
	binaryStr = "Binary"
	plainStr  = "Lua console"
)

// Protocol defines a set of supported protocols for a connect.
type Protocol int

const (
	// IPROTO.
	BinaryProtocol Protocol = iota
	// Plain text messages.
	TextProtocol
)

// String returns a string representation of the protocol.
func (protocol Protocol) String() string {
	switch protocol {
	case BinaryProtocol:
		return binaryStr
	case TextProtocol:
		return plainStr
	default:
		panic("Unknown protocol")
	}
}

// GetProtocol gets a protocol name from the reader greeting. See:
// https://github.com/tarantool/tarantool/blob/8dcefeb2bf5291487496d168cb81f5b6082a2af0/test/unit/xrow.cc#L92-L123
func GetProtocol(reader io.Reader) (Protocol, error) {
	greeting := make([]byte, greetingSize)
	if _, err := reader.Read(greeting); err != nil {
		err = fmt.Errorf("failed to read Tarantool greeting: %w", err)
		return BinaryProtocol, err
	}

	if protocol, ok := ParseProtocol(string(greeting)); !ok {
		err := fmt.Errorf("failed to parse Tarantool greeting: %s", greeting)
		return BinaryProtocol, err
	} else {
		return protocol, nil
	}
}

// ParseProtocol parses a protocol name from a Tarantool greeting.
func ParseProtocol(greeting string) (Protocol, bool) {
	if strings.HasPrefix(greeting, "Tarantool ") {
		switch {
		case strings.Contains(greeting, "("+binaryStr+")"):
			return BinaryProtocol, true
		case strings.Contains(greeting, "("+plainStr+")"):
			return TextProtocol, true
		}
	}
	return BinaryProtocol, false
}
