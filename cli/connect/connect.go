package connect

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"syscall"

	"github.com/tarantool/tt/cli/connector"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/yaml.v2"
)

// ConnectCtx contains information for connecting to the instance.
type ConnectCtx struct {
	// Username of the tarantool user.
	Username string
	// Password of the tarantool.user.
	Password string
	// SrcFile describes the source of code for the evaluation.
	SrcFile string
	// Language to use for execution.
	Language Language
	// SslKeyFile is a path to a private SSL key file.
	SslKeyFile string
	// SslCertFile is a path to an SSL certificate file.
	SslCertFile string
	// SslCaFile is a path to a trusted certificate authorities (CA) file.
	SslCaFile string
	// SslCiphers is a colon-separated (:) list of SSL cipher suites the
	// connection can use.
	SslCiphers string
	// Interactive mode is used.
	Interactive bool
}

const (
	// see https://github.com/tarantool/tarantool/blob/b53cb2aeceedc39f356ceca30bd0087ee8de7c16/src/box/lua/console.c#L265
	tarantoolWordSeparators = "\t\r\n !\"#$%&'()*+,-/;<=>?@[\\]^`{|}~"
)

func getConnOpts(connString string, connCtx ConnectCtx) connector.ConnectOpts {
	username := connCtx.Username
	password := connCtx.Password
	ssl := connector.SslOpts{
		KeyFile:  connCtx.SslKeyFile,
		CertFile: connCtx.SslCertFile,
		CaFile:   connCtx.SslCaFile,
		Ciphers:  connCtx.SslCiphers,
	}
	return connector.MakeConnectOpts(connString, username, password, ssl)
}

// getEvalCmd returns a command from the input source (file or stdin).
func getEvalCmd(connectCtx ConnectCtx) (string, error) {
	var cmd string

	if connectCtx.SrcFile == "-" {
		if !terminal.IsTerminal(syscall.Stdin) {
			cmdByte, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				return "", err
			}
			cmd = string(cmdByte)
		} else {
			return "", fmt.Errorf("can't use interactive input as a source file")
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
func Connect(connectCtx ConnectCtx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("should be specified one connection string")
	}

	connString := args[0]
	connOpts := getConnOpts(connString, connectCtx)

	if err := runConsole(connOpts, "", connectCtx.Language); err != nil {
		return fmt.Errorf("failed to run interactive console: %s", err)
	}

	return nil
}

// Eval executes the command on the remote instance (according to args).
func Eval(connectCtx ConnectCtx, args []string) ([]byte, error) {
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
		return nil, fmt.Errorf("unable to establish connection: %s", err)
	}
	defer conn.Close()

	// Change a language.
	if connectCtx.Language != DefaultLanguage {
		if err := ChangeLanguage(conn, connectCtx.Language); err != nil {
			return nil, fmt.Errorf("unable to change a language: %s", err)
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
		return fmt.Errorf("failed to create new console: %s", err)
	}
	defer console.Close()

	if err := console.Run(); err != nil {
		return fmt.Errorf("failed to start new console: %s", err)
	}

	return nil
}
