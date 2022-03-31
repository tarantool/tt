package connect

import (
	"fmt"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connector"
	"gopkg.in/yaml.v2"
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

// Eval executes the command on the remote instance (according to args).
func Eval(cmdCtx *cmdcontext.CmdCtx, args []string) ([]byte, error) {
	// Parse the arguments.
	connString := args[0]
	connOpts := getConnOpts(connString, cmdCtx)
	command := args[1]

	// Connecting to the instance.
	conn, err := connector.Connect(connOpts.Address, connOpts.Username, connOpts.Password)
	if err != nil {
		return nil, fmt.Errorf("Unable to establish connection: %s", err)
	}

	// Execution of the command.
	req := connector.EvalReq(evalFuncBody, command)
	res, err := conn.Exec(req)
	if err != nil {
		return nil, err
	}

	// Check that the result is encoded in YAML and convert it to bytes,
	// since the ""gopkg.in/yaml.v2" library handles YAML as an array
	// of bytes.
	resYAML := []byte((res[0]).(string))
	var checkMock interface{}
	if err = yaml.Unmarshal(resYAML, &checkMock); err != nil {
		return nil, err
	}

	return resYAML, nil
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
