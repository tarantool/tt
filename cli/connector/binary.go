package connector

import (
	"context"
	"fmt"

	"github.com/FZambia/tarantool"
)

func connectBinary(connOpts *ConnOpts) (*tarantool.Connection, error) {
	connectStr := fmt.Sprintf("%s://%s", connOpts.Network, connOpts.Address)

	binaryConn, err := tarantool.Connect(connectStr, tarantool.Opts{
		User:           connOpts.Username,
		Password:       connOpts.Password,
		SkipSchema:     true, // see https://github.com/FZambia/tarantool/issues/3
		RequestTimeout: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("Failed to connect: %s", err)
	}

	return binaryConn, nil
}

func initBinaryConn(conn *Conn, connOpts *ConnOpts) error {
	var err error

	if conn.binary, err = connectBinary(connOpts); err != nil {
		return err
	}

	conn.evalFunc = evalBinary
	conn.callFunc = callBinary

	return nil
}

func evalBinary(conn *Conn, funcBody string, args []interface{}, execOpts ExecOpts) ([]interface{}, error) {
	evalReq := tarantool.Eval(funcBody, args)
	return processTarantoolReqBinary(conn, evalReq, execOpts)
}

func callBinary(conn *Conn, funcName string, args []interface{}, execOpts ExecOpts) ([]interface{}, error) {
	callReq := tarantool.Call(funcName, args)
	return processTarantoolReqBinary(conn, callReq, execOpts)
}

func processTarantoolReqBinary(conn *Conn, req *tarantool.Request, execOpts ExecOpts) ([]interface{}, error) {
	if execOpts.PushCallback != nil {
		req.WithPush(func(r *tarantool.Response) {
			execOpts.PushCallback(r.Data[0])
		})
	}

	ctx := context.Background()
	var cancel context.CancelFunc

	if execOpts.ReadTimeout != 0 {
		ctx, cancel = context.WithTimeout(ctx, execOpts.ReadTimeout)
		defer cancel()
	}

	return execTarantoolReqBinary(ctx, conn.binary, req, execOpts.ResData)
}

func execTarantoolReqBinary(ctx context.Context, binaryConn *tarantool.Connection, req *tarantool.Request, resData interface{}) ([]interface{}, error) {
	var err error
	var resp *tarantool.Response

	if resData != nil {
		err = binaryConn.ExecTypedContext(ctx, req, resData)
	} else {
		resp, err = binaryConn.ExecContext(ctx, req)
	}

	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, nil
	}

	return resp.Data, nil
}
