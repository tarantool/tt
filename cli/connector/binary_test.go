package connector_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3"

	. "github.com/tarantool/tt/v3/cli/connector"
)

type binaryConnectorStub struct {
	tarantool.Connector

	err    error
	closed int
}

func (conn *binaryConnectorStub) Close() error {
	conn.closed++
	return conn.err
}

func TestNewBinaryConnector_implementsEvaler(t *testing.T) {
	var _ Evaler = NewBinaryConnector(nil)
}

func TestNewBinaryConnector_implementsConnector(t *testing.T) {
	var _ Connector = NewBinaryConnector(nil)
}

func TestBinaryConnector_Close(t *testing.T) {
	stub := &binaryConnectorStub{}
	conn := NewBinaryConnector(stub)

	require.NoError(t, conn.Close())
	assert.Equal(t, 1, stub.closed)
	require.NoError(t, conn.Close())
	assert.Equal(t, 2, stub.closed)
}

func TestBinaryConnector_Close_error(t *testing.T) {
	stub := &binaryConnectorStub{err: errAnyError}
	conn := NewBinaryConnector(stub)

	require.ErrorIs(t, conn.Close(), errAnyError)
	assert.Equal(t, 1, stub.closed)
}

func TestBinaryConnector_Close_nil(t *testing.T) {
	conn := NewBinaryConnector(nil)

	assert.NoError(t, conn.Close())
}
