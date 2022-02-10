package connector

import (
	"net"
)

func initPlainTextConn(conn *Conn, plainTextConn net.Conn) error {
	conn.plainText = plainTextConn

	conn.evalFunc = evalPlainText
	conn.callFunc = callPlainText

	return nil
}

func evalPlainText(conn *Conn, funcBody string, args []interface{}, execOpts ExecOpts) ([]interface{}, error) {
	evalPlainTextOpts := getEvalPlainTextOpts(execOpts)
	return evalPlainTextConn(conn.plainText, funcBody, args, evalPlainTextOpts)
}

func callPlainText(conn *Conn, funcName string, args []interface{}, execOpts ExecOpts) ([]interface{}, error) {
	evalPlainTextOpts := getEvalPlainTextOpts(execOpts)
	return callPlainTextConn(conn.plainText, funcName, args, evalPlainTextOpts)
}

func getEvalPlainTextOpts(execOpts ExecOpts) EvalPlainTextOpts {
	return EvalPlainTextOpts{
		PushCallback: execOpts.PushCallback,
		ReadTimeout:  execOpts.ReadTimeout,
		ResData:      execOpts.ResData,
	}
}
