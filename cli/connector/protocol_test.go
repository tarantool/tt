package connector_test

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/tarantool/tt/cli/connector"
)

func TestProtocol_String(t *testing.T) {
	cases := []struct {
		protocol Protocol
		expected string
		panic    bool
	}{
		{BinaryProtocol, "Binary", false},
		{TextProtocol, "Lua console", false},
		{Protocol(666), "Unknown protocol", true},
	}

	for _, c := range cases {
		t.Run(c.expected, func(t *testing.T) {
			if c.panic {
				f := func() { _ = c.protocol.String() }
				assert.PanicsWithValue(t, c.expected, f)
			} else {
				result := c.protocol.String()
				assert.Equal(t, c.expected, result)
			}
		})
	}
}

type greetingReadStub struct {
	io.Reader
	err  error
	data []byte
}

func (stub *greetingReadStub) Read(dst []byte) (int, error) {
	if stub.err != nil {
		return 0, stub.err
	}
	return copy(dst, stub.data), nil
}

func TestGetProtocol(t *testing.T) {
	// spell-checker:ignore asdasdasd asdasdasdqwe
	err := errors.New("any error")
	cases := []struct {
		greeting string
		err      error
		expected Protocol
		ok       bool
	}{
		{"", nil, BinaryProtocol, false},
		{"Tarantool", nil, BinaryProtocol, false},
		{"(Binary)", nil, BinaryProtocol, false},
		{"(Lua console)", nil, BinaryProtocol, false},
		{"Tarantool(Binary)", nil, BinaryProtocol, false},
		{"Tarantool(Lua console)", nil, BinaryProtocol, false},
		{"Tarantool (Binary)", nil, BinaryProtocol, true},
		{"Tarantool (Lua console)", nil, TextProtocol, true},
		{"Tarantool asdasdasd (Binary)", nil, BinaryProtocol, true},
		{"Tarantool asdasdasd (Lua console)", nil, TextProtocol, true},
		{"Tarantool asdasdasd (Binary)123123", nil, BinaryProtocol, true},
		{"Tarantool asdasdasd (Lua console)123123", nil, TextProtocol, true},
		{"Tarantool asdasdasdqwe(Binary)123123", nil, BinaryProtocol, true},
		{"Tarantool asdasdasdqwe(Lua console)123123", nil, TextProtocol, true},
		{"Tarantool (Binary)", err, BinaryProtocol, false},
		{"Tarantool (Lua console)", err, BinaryProtocol, false},
	}

	for _, c := range cases {
		t.Run(c.greeting, func(t *testing.T) {
			s := &greetingReadStub{err: c.err, data: []byte(c.greeting)}
			p, err := GetProtocol(s)
			assert.Equal(t, c.expected, p)
			if c.err != nil {
				assert.ErrorContains(t, err, "failed to read Tarantool greeting:")
				assert.ErrorContains(t, err, c.err.Error())
			} else if !c.ok {
				assert.ErrorContains(t, err, "failed to parse Tarantool greeting:")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
