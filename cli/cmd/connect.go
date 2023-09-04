package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmd/internal"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"golang.org/x/crypto/ssh/terminal"
)

const (
	usernameEnv = "TT_CLI_USERNAME"
	passwordEnv = "TT_CLI_PASSWORD"
	userpassRe  = `[^@:/]+:[^@:/]+`

	// uriPathPrefixRe is a regexp for a path prefix in uri, such as `scheme://path``.
	uriPathPrefixRe = `((~?/+)|((../+)*))?`

	// systemPathPrefixRe is a regexp for a path prefix to use without scheme.
	systemPathPrefixRe = `(([\.~]?/+)|((../+)+))`
)

var (
	connectUser        string
	connectPassword    string
	connectFile        string
	connectLanguage    string
	connectSslKeyFile  string
	connectSslCertFile string
	connectSslCaFile   string
	connectSslCiphers  string
	connectInteractive bool
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
			"* " + usernameEnv + " - specifies a username\n" +
			"* " + passwordEnv + " - specifies a password\n" +
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
			return internal.ValidArgsFunction(
				cliOpts, &cmdCtx, cmd, toComplete,
				running.ExtractActiveAppNames,
				running.ExtractActiveInstanceNames)
		},
	}

	connectCmd.Flags().StringVarP(&connectUser, "username", "u", "", "username")
	connectCmd.Flags().StringVarP(&connectPassword, "password", "p", "", "password")
	connectCmd.Flags().StringVarP(&connectFile, "file", "f", "",
		`file to read the script for evaluation. "-" - read the script from stdin`)
	connectCmd.Flags().StringVarP(&connectLanguage, "language", "l",
		connect.DefaultLanguage.String(), `language: lua or sql`)
	connectCmd.Flags().StringVarP(&connectSslKeyFile, "sslkeyfile", "",
		connect.DefaultLanguage.String(), `path to a private SSL key file`)
	connectCmd.Flags().StringVarP(&connectSslCertFile, "sslcertfile", "",
		connect.DefaultLanguage.String(), `path to an SSL certificate file`)
	connectCmd.Flags().StringVarP(&connectSslCaFile, "sslcafile", "",
		connect.DefaultLanguage.String(),
		`path to a trusted certificate authorities (CA) file`)
	connectCmd.Flags().StringVarP(&connectSslCiphers, "sslciphers", "",
		connect.DefaultLanguage.String(),
		`colon-separated (:) list of SSL cipher suites the connection`)
	connectCmd.Flags().BoolVarP(&connectInteractive, "interactive", "i",
		false, `enter interactive mode after executing 'FILE'`)

	return connectCmd
}

// isBaseURI returns true if a string is a valid URI.
func isBaseURI(str string) bool {
	// tcp://host:port
	// host:port
	tcpReStr := `(tcp://)?([\w\\.-]+:\d+)`
	// unix://../path
	// unix://~/path
	// unix:///path
	// unix://path
	unixReStr := `unix://` + uriPathPrefixRe + `[^\./@]+[^@]*`
	// ../path
	// ~/path
	// /path
	// ./path
	pathReStr := systemPathPrefixRe + `[^\./].*`

	uriReStr := "^((" + tcpReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
	uriRe := regexp.MustCompile(uriReStr)
	return uriRe.MatchString(str)
}

// isCredentialsURI returns true if a string is a valid credentials URI.
func isCredentialsURI(str string) bool {
	// tcp://user:password@host:port
	// user:password@host:port
	tcpReStr := `(tcp://)?` + userpassRe + `@([\w\.-]+:\d+)`
	// unix://user:password@../path
	// unix://user:password@~/path
	// unix://user:password@/path
	// unix://user:password@path
	unixReStr := `unix://` + userpassRe + `@` + uriPathPrefixRe + `[^\./@]+.*`
	// user:password@../path
	// user:password@~/path
	// user:password@/path
	// user:password@./path
	pathReStr := userpassRe + `@` + systemPathPrefixRe + `[^\./].*`

	uriReStr := "^((" + tcpReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
	uriRe := regexp.MustCompile(uriReStr)
	return uriRe.MatchString(str)
}

// parseBaseURI parses an URI and returns:
// (network, address)
func parseBaseURI(uri string) (string, string) {
	var network, address string
	uriLen := len(uri)

	switch {
	case uriLen > 0 && (uri[0] == '.' || uri[0] == '/' || uri[0] == '~'):
		network = connector.UnixNetwork
		address = uri
	case uriLen >= 7 && uri[0:7] == "unix://":
		network = connector.UnixNetwork
		address = uri[7:]
	case uriLen >= 6 && uri[0:6] == "tcp://":
		network = connector.TCPNetwork
		address = uri[6:]
	default:
		network = connector.TCPNetwork
		address = uri
	}

	// In the case of a complex uri, shell expansion does not occur, so do it manually.
	if network == connector.UnixNetwork &&
		strings.HasPrefix(address, "~/") {
		if homeDir, err := os.UserHomeDir(); err == nil {
			address = filepath.Join(homeDir, address[2:])
		}
	}

	return network, address
}

// parseCredentialsURI parses a URI with credentials and returns:
// (URI without credentials, user, password)
func parseCredentialsURI(str string) (string, string, string) {
	if !isCredentialsURI(str) {
		return str, "", ""
	}

	re := regexp.MustCompile(userpassRe + `@`)
	// Split the string into two parts by credentials to create a string
	// without the credentials.
	split := re.Split(str, 2)
	newStr := split[0] + split[1]

	// Parse credentials.
	credentialsStr := re.FindString(str)
	credentialsLen := len(credentialsStr) - 1 // We don't need a last '@'.
	credentialsSlice := strings.Split(credentialsStr[:credentialsLen], ":")

	return newStr, credentialsSlice[0], credentialsSlice[1]
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
		if connectCtx.Username != "" || connectCtx.Password != "" {
			err = fmt.Errorf("username and password are not supported" +
				" with a connection via a control socket")
			return
		}
		connOpts = makeConnOpts(
			connector.UnixNetwork, runningCtx.Instances[0].ConsoleSocket, *connectCtx,
		)
	} else if isCredentialsURI(args[0]) {
		if connectCtx.Username != "" || connectCtx.Password != "" {
			err = fmt.Errorf("username and password are specified with" +
				" flags and a URI")
			return
		}
		newURI, user, pass := parseCredentialsURI(args[0])
		network, address := parseBaseURI(newURI)
		connectCtx.Username = user
		connectCtx.Password = pass
		connOpts = makeConnOpts(network, address, *connectCtx)
	} else if isBaseURI(args[0]) {
		// Environment variables do not overwrite values.
		if connectCtx.Username == "" {
			connectCtx.Username = os.Getenv(usernameEnv)
		}
		if connectCtx.Password == "" {
			connectCtx.Password = os.Getenv(passwordEnv)
		}
		network, address := parseBaseURI(args[0])
		connOpts = makeConnOpts(network, address, *connectCtx)
	} else {
		err = fillErr
		return
	}
	connectCtx.ConnectTarget = args[0]
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
	}

	var ok bool
	if connectCtx.Language, ok = connect.ParseLanguage(connectLanguage); !ok {
		return util.NewArgError(fmt.Sprintf("unsupported language: %s", connectLanguage))
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
