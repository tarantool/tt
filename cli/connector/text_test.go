package connector_test

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	. "github.com/tarantool/tt/cli/connector"
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

	assert.NoError(t, conn.Close())
	assert.Equal(t, 1, stub.closed)
	assert.NoError(t, conn.Close())
	assert.Equal(t, 2, stub.closed)
}

func TestTextConnector_Close_error(t *testing.T) {
	const errMsg = "any error"
	stub := &plainConnectorStub{err: errors.New(errMsg)}
	conn := NewTextConnector(stub)

	assert.EqualError(t, conn.Close(), errMsg)
	assert.Equal(t, 1, stub.closed)
}

func TestTextConnector_Close_nil(t *testing.T) {
	conn := NewTextConnector(nil)

	assert.NoError(t, conn.Close())
}
