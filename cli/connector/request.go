package connector

import (
	"time"
)

type Request struct {
	execFunc      func(conn *Conn) ([]interface{}, error)
	execTypedFunc func(conn *Conn, resData interface{}) error

	pushCallback func(interface{})
	readTimeout  time.Duration
}

func (req *Request) SetPushCallback(pushCallback func(interface{})) *Request {
	req.pushCallback = pushCallback
	return req
}

func (req *Request) SetReadTimeout(readTimeout time.Duration) *Request {
	req.readTimeout = readTimeout
	return req
}

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

func CallReq(funcName string, args ...interface{}) *Request {
	req := &Request{}

	req.execFunc = func(conn *Conn) ([]interface{}, error) {
		return conn.callFunc(conn, funcName, args, ExecOpts{
			PushCallback: req.pushCallback,
			ReadTimeout:  req.readTimeout,
		})
	}

	req.execTypedFunc = func(conn *Conn, resData interface{}) error {
		_, err := conn.callFunc(conn, funcName, args, ExecOpts{
			PushCallback: req.pushCallback,
			ReadTimeout:  req.readTimeout,
			ResData:      resData,
		})

		return err
	}

	return req
}
