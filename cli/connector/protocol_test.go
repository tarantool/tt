package connector_test

import (
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/tarantool/tt/v3/cli/connector"
)

var (
	errAnyError = errors.New("any error")
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

	for _, testCase := range cases {
		t.Run(testCase.expected, func(t *testing.T) {
			if testCase.panic {
				f := func() { _ = testCase.protocol.String() }
				assert.PanicsWithValue(t, testCase.expected, f)
			} else {
				result := testCase.protocol.String()
				assert.Equal(t, testCase.expected, result)
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
	err := errAnyError
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

	for _, testCase := range cases {
		t.Run(testCase.greeting, func(t *testing.T) {
			s := &greetingReadStub{err: testCase.err, data: []byte(testCase.greeting)}
			p, err := GetProtocol(s)
			assert.Equal(t, testCase.expected, p)

			switch {
			case testCase.err != nil:
				require.ErrorContains(t, err, "failed to read Tarantool greeting:")
				require.ErrorContains(t, err, testCase.err.Error())
			case !testCase.ok:
				require.ErrorContains(t, err, "failed to parse Tarantool greeting:")
			default:
				require.NoError(t, err)
			}
		})
	}
}
