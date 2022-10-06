package connector_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/go-tarantool"

	. "github.com/tarantool/tt/cli/connector"
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

	assert.NoError(t, conn.Close())
	assert.Equal(t, 1, stub.closed)
	assert.NoError(t, conn.Close())
	assert.Equal(t, 2, stub.closed)
}

func TestBinaryConnector_Close_error(t *testing.T) {
	const errMsg = "any error"
	stub := &binaryConnectorStub{err: errors.New(errMsg)}
	conn := NewBinaryConnector(stub)

	assert.EqualError(t, conn.Close(), errMsg)
	assert.Equal(t, 1, stub.closed)
}

func TestBinaryConnector_Close_nil(t *testing.T) {
	conn := NewBinaryConnector(nil)

	assert.NoError(t, conn.Close())
}
