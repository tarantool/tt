package connect

import (
	"fmt"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connector"
)

const (
	// see https://github.com/tarantool/tarantool/blob/b53cb2aeceedc39f356ceca30bd0087ee8de7c16/src/box/lua/console.c#L265
	tarantoolWordSeparators = "\t\r\n !\"#$%&'()*+,-/;<=>?@[\\]^`{|}~"
)

func getConnOpts(connString string, cmdCtx *cmdcontext.CmdCtx) *connector.ConnOpts {
	return connector.GetConnOpts(connString, cmdCtx.Connect.Username, cmdCtx.Connect.Password)
}

// Connect establishes a connection to the instance and starts the console.
func Connect(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("Should be specified one connection string")
	}

	connString := args[0]
	connOpts := getConnOpts(connString, cmdCtx)

	if err := runConsole(connOpts, ""); err != nil {
		return fmt.Errorf("Failed to run interactive console: %s", err)
	}

	return nil
}

// runConsole run a new console.
func runConsole(connOpts *connector.ConnOpts, title string) error {
	console, err := NewConsole(connOpts, title)
	if err != nil {
		return fmt.Errorf("Failed to create new console: %s", err)
	}
	defer console.Close()

	if err := console.Run(); err != nil {
		return fmt.Errorf("Failed to start new console: %s", err)
	}

	return nil
}
