package connector

import (
	"time"
)

// Request describes ways and conditions of interaction with the instance.
type Request struct {
	execFunc      func(conn *Conn) ([]interface{}, error)
	execTypedFunc func(conn *Conn, resData interface{}) error

	pushCallback func(interface{})
	readTimeout  time.Duration
}

// SetPushCallback sets the callback that will be called when a "push" message is received.
func (req *Request) SetPushCallback(pushCallback func(interface{})) *Request {
	req.pushCallback = pushCallback
	return req
}

// SetReadTimeout sets timeout for the operation.
func (req *Request) SetReadTimeout(readTimeout time.Duration) *Request {
	req.readTimeout = readTimeout
	return req
}

// EvalReq prepares "Request" to evaluate an expression.
func EvalReq(funcBody string, args ...interface{}) *Request {
	if args == nil {
		args = []interface{}{}
	}

	req := &Request{}

	req.execFunc = func(conn *Conn) ([]interface{}, error) {
		return conn.evalFunc(conn, funcBody, args, ExecOpts{
			PushCallback: req.pushCallback,
			ReadTimeout:  req.readTimeout,
		})
	}

	req.execTypedFunc = func(conn *Conn, resData interface{}) error {
		_, err := conn.evalFunc(conn, funcBody, args, ExecOpts{
			PushCallback: req.pushCallback,
			ReadTimeout:  req.readTimeout,
			ResData:      resData,
		})

		return err
	}

	return req
}
