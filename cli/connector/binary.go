package connector

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/tarantool/go-tarantool"
	_ "github.com/tarantool/go-tarantool/datetime"
	_ "github.com/tarantool/go-tarantool/decimal"
	_ "github.com/tarantool/go-tarantool/uuid"
)

// BinaryConnector implements Connector interface for a connection that sends
// and receives data via IPROTO.
type BinaryConnector struct {
	conn tarantool.Connector
}

// NewBinaryConnector creates a new BinaryConnector object. The object will
// close the tarantool.Connector argument in Close() call.
func NewBinaryConnector(conn tarantool.Connector) *BinaryConnector {
	return &BinaryConnector{
		conn: conn,
	}
}

// Eval sends an eval request.
func (conn *BinaryConnector) Eval(expr string, args []interface{},
	opts RequestOpts) ([]interface{}, error) {
	// Create a request.
	evalReq := tarantool.NewEvalRequest(expr).Args(args)
	if opts.ReadTimeout != 0 {
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, opts.ReadTimeout)
		defer cancel()

		evalReq = evalReq.Context(ctx)
	}

	// Execute the request.
	var err error
	var response *tarantool.Response
	future := conn.conn.Do(evalReq)
	if opts.PushCallback != nil {
		var timeout time.Duration
		if opts.ReadTimeout != 0 {
			timeout = opts.ReadTimeout
		} else {
			timeout = time.Duration(math.MaxInt64)
		}
		for it := future.GetIterator().WithTimeout(timeout); it.Next(); {
			if err := it.Err(); err != nil {
				return nil, replaceContextDone(err)
			}
			response := it.Value()
			if response.Code != tarantool.PushCode {
				break
			}
			opts.PushCallback(response.Data[0])
		}
	}

	// Get responseonse.
	if opts.ResData != nil {
		err = future.GetTyped(opts.ResData)
	} else {
		response, err = future.Get()
	}

	if err != nil {
		return nil, replaceContextDone(err)
	}

	if response == nil {
		return nil, nil
	}

	return response.Data, nil
}

// Close closes the tarantool.Connector created from.
func (conn *BinaryConnector) Close() error {
	if conn.conn != nil {
		return conn.conn.Close()
	}
	return nil
}

// replaceContextDone replaces "context done" error by "i/o timeout" error.
func replaceContextDone(err error) error {
	if err == nil || err.Error() != "context is done" {
		return err
	}
	return errors.New("i/o timeout")
}
