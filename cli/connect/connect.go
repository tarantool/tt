package connect

import (
	"fmt"

	"github.com/tarantool/tt/cli/cmdcontext"
)

const (
	// see https://github.com/tarantool/tarantool/blob/b53cb2aeceedc39f356ceca30bd0087ee8de7c16/src/box/lua/console.c#L265
	tarantoolWordSeparators = "\t\r\n !\"#$%&'()*+,-/;<=>?@[\\]^`{|}~"
)

func Connect(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("Should be specified one connection string")
	}

	connString := args[0]

	connOpts, err := getConnOpts(connString, cmdCtx)
	if err != nil {
		return fmt.Errorf("Failed to get connection opts: %s", err)
	}

	if err := runConsole(connOpts, ""); err != nil {
		return fmt.Errorf("Failed to run interactive console: %s", err)
	}

	return nil
}

func runConsole(connOpts *ConnOpts, title string) error {
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
