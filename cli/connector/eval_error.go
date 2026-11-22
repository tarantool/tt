package connector

import (
	"errors"

	"github.com/tarantool/go-iproto"
	"github.com/tarantool/go-tarantool/v3"
)

// LuaError returns the message of the Lua error an eval of a Connector
// ended with, and whether err is one: over the console the error it
// answered with, without the position in the chunk of the eval; over iproto
// the error of the eval, ER_PROC_LUA, whose message is that of the error the
// expression raised, as the server gives it, with any position in it. An
// error of the connection, and one of the server that is not of the Lua
// code, such as a request the user has no right to, is no Lua error.
func LuaError(err error) (string, bool) {
	console, ok := errors.AsType[evaluationError](err)
	if ok {
		return string(console), true
	}

	server, ok := errors.AsType[tarantool.ServerError](err)
	if ok && server.Code == iproto.ER_PROC_LUA {
		return server.Msg, true
	}

	return "", false
}
