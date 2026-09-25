package connector_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	. "github.com/tarantool/tt/v3/cli/connector"
)

type plainConnectorStub struct {
	net.Conn

	err    error
	closed int
}

func (conn *plainConnectorStub) Close() error {
	conn.closed++
	return conn.err
}

func TestNewTextConnector_implementsEvaler(t *testing.T) {
	var _ Evaler = NewTextConnector(nil)
}

func TestNewTextConnector_implementsConnector(t *testing.T) {
	var _ Connector = NewTextConnector(nil)
}

func TestTextConnector_Close(t *testing.T) {
	stub := &plainConnectorStub{}
	conn := NewTextConnector(stub)

	require.NoError(t, conn.Close())
	assert.Equal(t, 1, stub.closed)
	require.NoError(t, conn.Close())
	assert.Equal(t, 2, stub.closed)
}

func TestTextConnector_Close_error(t *testing.T) {
	stub := &plainConnectorStub{err: errAnyError}
	conn := NewTextConnector(stub)

	require.ErrorIs(t, conn.Close(), errAnyError)
	assert.Equal(t, 1, stub.closed)
}

func TestTextConnector_Close_nil(t *testing.T) {
	conn := NewTextConnector(nil)

	assert.NoError(t, conn.Close())
}
