package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/formatter"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	libconnect "github.com/tarantool/tt/lib/connect"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	connectUser        string
	connectPassword    string
	connectFile        string
	connectLanguage    string
	connectFormat      string
	connectSslKeyFile  string
	connectSslCertFile string
	connectSslCaFile   string
	connectSslCiphers  string
	connectInteractive bool
	connectBinary      bool
)

// NewConnectCmd creates connect command.
func NewConnectCmd() *cobra.Command {
	var connectCmd = &cobra.Command{
		Use: "connect (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)" +
			" [flags] [-f <FILE>] [-- ARGS]\n" +
			"  COMMAND | tt connect (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)" +
			" [flags]\n" +
			"  COMMAND | tt connect (<APP_NAME> | <APP_NAME:INSTANCE_NAME> | <URI>)" +
			" [flags] [-f-] [-- ARGS]\n\n" +
			"  The URI can be specified in the following formats:\n" +
			"  * [tcp://][username:password@][host:port]\n" +
			"  * [unix://][username:password@]socketpath\n" +
			"  To specify relative path without `unix://` use `./`.\n\n" +
			"  Available commands:\n" +
			"  * \\shortcuts - get the full list of available shortcuts\n" +
			"  * \\set language <language> - set language (lua or sql)\n" +
			"  * \\set output <format> - set output format (lua[,line|block] or yaml)\n" +
			"  * \\set delimiter <delimiter> - set expression delimiter\n" +
			"  * \\help - show available backslash commands\n" +
			"  * \\quit - quit interactive console",
		Short: "Connect to the tarantool instance",
		Long: "Connect to the tarantool instance.\n\n" +
			"The command supports the following environment variables:\n\n" +
			"* " + libconnect.TarantoolUsernameEnv + " - specifies a username\n" +
			"* " + libconnect.TarantoolPasswordEnv + " - specifies a password\n" +
			"\n" +
			"You could pass command line arguments to the interpreted SCRIPT" +
			" or COMMAND passed via -f flag:\n\n" +
			`echo "print(...)" | tt connect user:pass@localhost:3013 -f- 1, 2, 3`,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalConnectModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.MinimumNArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			validArgs, _ := internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractActiveAppNames,
				running.ExtractActiveInstanceNames)
			return validArgs, cobra.ShellCompDirectiveDefault
		},
	}

	connectCmd.Flags().StringVarP(&connectUser, "username", "u", "", "username")
	connectCmd.Flags().StringVarP(&connectPassword, "password", "p", "", "password")
	connectCmd.Flags().StringVarP(&connectFile, "file", "f", "",
		`file to read the script for evaluation. "-" - read the script from stdin`)
	connectCmd.Flags().StringVarP(&connectLanguage, "language", "l",
		connect.DefaultLanguage.String(), `language: lua or sql`)
	connectCmd.Flags().StringVarP(&connectFormat, "outputformat", "x",
		formatter.DefaultFormat.String(), `output format: yaml, lua, table or ttable`)
	connectCmd.Flags().StringVar(&connectSslKeyFile, "sslkeyfile", "",
		`path to a private SSL key file`)
	connectCmd.Flags().StringVar(&connectSslCertFile, "sslcertfile", "",
		`path to an SSL certificate file`)
	connectCmd.Flags().StringVar(&connectSslCaFile, "sslcafile", "",
		`path to a trusted certificate authorities (CA) file`)
	connectCmd.Flags().StringVar(&connectSslCiphers, "sslciphers", "",
		`colon-separated (:) list of SSL cipher suites the connection`)
	connectCmd.Flags().BoolVarP(&connectInteractive, "interactive", "i",
		false, `enter interactive mode after executing 'FILE'`)
	connectCmd.Flags().BoolVarP(&connectBinary, "binary", "",
		false, `connect to instance using binary port`)

	return connectCmd
}

// makeConnOpts makes and returns connect options from the arguments.
func makeConnOpts(network, address string, connCtx connect.ConnectCtx) connector.ConnectOpts {
	ssl := connector.SslOpts{
		KeyFile:  connCtx.SslKeyFile,
		CertFile: connCtx.SslCertFile,
		CaFile:   connCtx.SslCaFile,
		Ciphers:  connCtx.SslCiphers,
	}
	return connector.ConnectOpts{
		Network:  network,
		Address:  address,
		Username: connCtx.Username,
		Password: connCtx.Password,
		Ssl:      ssl,
	}
}

// resolveConnectOpts tries to resolve the first passed argument as an instance
// name to replace it with a control socket or as a URI with/without
// credentials.
// It returns connection options and the remaining args.
func resolveConnectOpts(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts,
	connectCtx *connect.ConnectCtx, args []string) (
	connOpts connector.ConnectOpts, newArgs []string, err error) {

	newArgs = args[1:]
	// FillCtx returns error if no instances found.
	var runningCtx running.RunningCtx
	if fillErr := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); fillErr == nil {
		if len(runningCtx.Instances) > 1 {
			err = fmt.Errorf("specify instance name")
			return
		}
		if (connectCtx.Username != "" || connectCtx.Password != "") && !connectCtx.Binary {
			err = fmt.Errorf("username and password are not supported" +
				" with a connection via a control socket")
			return
		}
		if connectCtx.Binary {
			connOpts = makeConnOpts(
				connector.UnixNetwork, runningCtx.Instances[0].BinaryPort, *connectCtx,
			)
		} else {
			connOpts = makeConnOpts(
				connector.UnixNetwork, runningCtx.Instances[0].ConsoleSocket, *connectCtx,
			)
		}
	} else if libconnect.IsCredentialsURI(args[0]) {
		if connectCtx.Username != "" || connectCtx.Password != "" {
			err = fmt.Errorf("username and password are specified with" +
				" flags and a URI")
			return
		}
		newURI, user, pass := libconnect.ParseCredentialsURI(args[0])
		network, address := libconnect.ParseBaseURI(newURI)
		connectCtx.Username = user
		connectCtx.Password = pass
		connOpts = makeConnOpts(network, address, *connectCtx)
		connectCtx.ConnectTarget = newURI
	} else if libconnect.IsBaseURI(args[0]) {
		// Environment variables do not overwrite values.
		if connectCtx.Username == "" {
			connectCtx.Username = os.Getenv(libconnect.TarantoolUsernameEnv)
		}
		if connectCtx.Password == "" {
			connectCtx.Password = os.Getenv(libconnect.TarantoolPasswordEnv)
		}
		network, address := libconnect.ParseBaseURI(args[0])
		connOpts = makeConnOpts(network, address, *connectCtx)
	} else {
		err = fillErr
		return
	}
	if connectCtx.ConnectTarget == "" {
		connectCtx.ConnectTarget = args[0]
	}
	return
}

// internalConnectModule is a default connect module.
func internalConnectModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	connectCtx := connect.ConnectCtx{
		Username:    connectUser,
		Password:    connectPassword,
		SrcFile:     connectFile,
		SslKeyFile:  connectSslKeyFile,
		SslCertFile: connectSslCertFile,
		SslCaFile:   connectSslCaFile,
		SslCiphers:  connectSslCiphers,
		Interactive: connectInteractive,
		Binary:      connectBinary,
	}

	var ok bool
	if connectCtx.Language, ok = connect.ParseLanguage(connectLanguage); !ok {
		return util.NewArgError(fmt.Sprintf("unsupported language: %s", connectLanguage))
	}
	if connectCtx.Format, ok = formatter.ParseFormat(connectFormat); !ok {
		return util.NewArgError(fmt.Sprintf("unsupported output format: %s", connectFormat))
	}

	connOpts, newArgs, err := resolveConnectOpts(cmdCtx, cliOpts, &connectCtx, args)
	if err != nil {
		return err
	}

	if connectFile != "" {
		res, err := connect.Eval(connectCtx, connOpts, newArgs)
		if err != nil {
			return err
		}
		// "Println" is used instead of "log..." to print the result without
		// any decoration.
		fmt.Println(string(res))
		if !connectInteractive || !terminal.IsTerminal(syscall.Stdin) {
			return nil
		}
	} else if len(newArgs) != 0 {
		return fmt.Errorf("should be specified one connection string")
	}

	if terminal.IsTerminal(syscall.Stdin) {
		log.Info("Connecting to the instance...")
	}
	if err := connect.Connect(connectCtx, connOpts); err != nil {
		return err
	}

	return nil
}
