package connector

import (
	"net"
)

// TextConnector implements Connector interface for a connection that sends
// and receives data as a plain text.
type TextConnector struct {
	conn net.Conn
}

// NewTextConnector creates a new TextConnector object. The object will close
// the net.Conn argument in Close() call.
func NewTextConnector(conn net.Conn) *TextConnector {
	return &TextConnector{
		conn: conn,
	}
}

// Eval sends an eval request.
func (conn *TextConnector) Eval(expr string, args []interface{},
	opts RequestOpts) ([]interface{}, error) {
	evalOpts := EvalPlainTextOpts{
		PushCallback: opts.PushCallback,
		ReadTimeout:  opts.ReadTimeout,
		ResData:      opts.ResData,
	}
	return evalPlainTextConn(conn.conn, expr, args, evalOpts)
}

// Close closes the net.Conn created from.
func (conn *TextConnector) Close() error {
	if conn.conn != nil {
		return conn.conn.Close()
	}
	return nil
}
