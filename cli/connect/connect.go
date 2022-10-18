package connect

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"syscall"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connector"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"
)

const (
	// see https://github.com/tarantool/tarantool/blob/b53cb2aeceedc39f356ceca30bd0087ee8de7c16/src/box/lua/console.c#L265
	tarantoolWordSeparators = "\t\r\n !\"#$%&'()*+,-/;<=>?@[\\]^`{|}~"
)

func getConnOpts(connString string, connCtx cmdcontext.ConnectCtx) connector.ConnectOpts {
	username := connCtx.Username
	password := connCtx.Password
	return connector.MakeConnectOpts(connString, username, password)
}

// getEvalCmd returns a command from the input source (file or stdin).
func getEvalCmd(connectCtx cmdcontext.ConnectCtx) (string, error) {
	var cmd string

	if connectCtx.SrcFile == "-" {
		if !terminal.IsTerminal(syscall.Stdin) {
			cmdByte, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return "", err
			}
			cmd = string(cmdByte)
		} else {
			return "", fmt.Errorf("Can't use interactive input as a source file")
		}
	} else {
		cmdPath := path.Clean(connectCtx.SrcFile)
		if _, err := os.Stat(cmdPath); err == nil {
			cmdByte, err := ioutil.ReadFile(cmdPath)
			if err != nil {
				return "", err
			}
			cmd = string(cmdByte)
		}
	}

	return cmd, nil
}

// Connect establishes a connection to the instance and starts the console.
func Connect(connectCtx cmdcontext.ConnectCtx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("Should be specified one connection string")
	}

	lang, ok := ParseLanguage(connectCtx.Language)
	if !ok {
		return fmt.Errorf("Unsupported language: %s", connectCtx.Language)
	}

	connString := args[0]
	connOpts := getConnOpts(connString, connectCtx)

	if err := runConsole(connOpts, "", lang); err != nil {
		return fmt.Errorf("Failed to run interactive console: %s", err)
	}

	return nil
}

// Eval executes the command on the remote instance (according to args).
func Eval(connectCtx cmdcontext.ConnectCtx, args []string) ([]byte, error) {
	lang, ok := ParseLanguage(connectCtx.Language)
	if !ok {
		return nil, fmt.Errorf("Unsupported language: %s", connectCtx.Language)
	}

	// Parse the arguments.
	connString := args[0]
	connOpts := getConnOpts(connString, connectCtx)
	command, err := getEvalCmd(connectCtx)
	if err != nil {
		return nil, err
	}

	// Connecting to the instance.
	conn, err := connector.Connect(connOpts)
	if err != nil {
		return nil, fmt.Errorf("Unable to establish connection: %s", err)
	}
	defer conn.Close()

	// Change a language.
	if lang != DefaultLanguage {
		if err := ChangeLanguage(conn, lang); err != nil {
			return nil, fmt.Errorf("Unable to change a language: %s", err)
		}
	}

	// Execution of the command.
	response, err := conn.Eval(evalFuncBody,
		[]interface{}{command},
		connector.RequestOpts{},
	)
	if err != nil {
		return nil, err
	}

	// Check that the result is encoded in YAML and convert it to bytes,
	// since the ""gopkg.in/yaml.v2" library handles YAML as an array
	// of bytes.
	resYAML := []byte((response[0]).(string))
	var checkMock interface{}
	if err = yaml.Unmarshal(resYAML, &checkMock); err != nil {
		return nil, err
	}

	return resYAML, nil
}

// runConsole run a new console.
func runConsole(connOpts connector.ConnectOpts, title string, lang Language) error {
	console, err := NewConsole(connOpts, title, lang)
	if err != nil {
		return fmt.Errorf("Failed to create new console: %s", err)
	}
	defer console.Close()

	if err := console.Run(); err != nil {
		return fmt.Errorf("Failed to start new console: %s", err)
	}

	return nil
}
