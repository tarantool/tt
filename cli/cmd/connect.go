package cmd

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"syscall"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/modules"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
	"golang.org/x/crypto/ssh/terminal"
)

const (
	usernameEnv = "TT_CLI_USERNAME"
	passwordEnv = "TT_CLI_PASSWORD"
	userpassRe  = `[^@:/]+:[^@:/]+`
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
			" [flags] [-f-] [-- ARGS]",
		Short: "Connect to the tarantool instance",
		Long: "Connect to the tarantool instance.\n\n" +
			"The command supports the following environment variables:\n\n" +
			"* " + usernameEnv + " - specifies a username\n" +
			"* " + passwordEnv + " - specifies a password\n" +
			"\n" +
			"You could pass command line arguments to the interpreted SCRIPT" +
			" or COMMAND passed via -f flag:\n\n" +
			`echo "print(...)" | tt connect user@pass:localhost:3013 -f- 1, 2, 3`,
		Run: func(cmd *cobra.Command, args []string) {
			cmdCtx.CommandName = cmd.Name()
			err := modules.RunCmd(&cmdCtx, cmd.CommandPath(), &modulesInfo,
				internalConnectModule, args)
			handleCmdErr(cmd, err)
		},
		Args: cobra.MinimumNArgs(1),
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
	// unix:///path
	// unix://path
	unixReStr := `unix://[./]*[^\./@]+[^@]*`
	// ./path
	// /path
	pathReStr := `\.?/[^\./].*`

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
	// unix://user:password@/path
	// unix://user:password@path
	unixReStr := `unix://` + userpassRe + `@[./@]*[^\./@]+.*`
	// user:password@./path
	// user:password@/path
	pathReStr := userpassRe + `@\.?/[^\./].*`

	uriReStr := "^((" + tcpReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
	uriRe := regexp.MustCompile(uriReStr)
	return uriRe.MatchString(str)
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

// resolveConnectOpts tries to resolve the first passed argument as an instance
// name to replace it with a control socket or as a URI with/without
// credentials.
func resolveConnectOpts(cmdCtx *cmdcontext.CmdCtx, cliOpts *config.CliOpts,
	connectCtx *connect.ConnectCtx, args []string) ([]string, error) {
	newArgs := args

	// FillCtx returns error if no instances found.
	var runningCtx running.RunningCtx
	if fillErr := running.FillCtx(cliOpts, cmdCtx, &runningCtx, args); fillErr == nil {
		if len(runningCtx.Instances) > 1 {
			return newArgs, fmt.Errorf("specify instance name")
		}
		if connectCtx.Username != "" || connectCtx.Password != "" {
			return newArgs, fmt.Errorf("username and password are not supported" +
				" with a connection via a control socket")
		}
		newArgs[0] = runningCtx.Instances[0].ConsoleSocket
		return newArgs, nil
	} else if isCredentialsURI(newArgs[0]) {
		if connectCtx.Username != "" || connectCtx.Password != "" {
			return newArgs, fmt.Errorf("username and password are specified with" +
				" flags and a URI")
		}
		uri, user, pass := parseCredentialsURI(newArgs[0])
		newArgs[0] = uri
		connectCtx.Username = user
		connectCtx.Password = pass
		return newArgs, nil
	} else if isBaseURI(newArgs[0]) {
		// Environment variables do not overwrite values.
		if connectCtx.Username == "" {
			connectCtx.Username = os.Getenv(usernameEnv)
		}
		if connectCtx.Password == "" {
			connectCtx.Password = os.Getenv(passwordEnv)
		}
		return newArgs, nil
	} else {
		return newArgs, fillErr
	}
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

	newArgs, err := resolveConnectOpts(cmdCtx, cliOpts, &connectCtx, args)
	if err != nil {
		return err
	}

	if connectFile != "" {
		res, err := connect.Eval(connectCtx, newArgs)
		if err != nil {
			return err
		}
		// "Println" is used instead of "log..." to print the result without
		// any decoration.
		fmt.Println(string(res))
		if !connectInteractive || !terminal.IsTerminal(syscall.Stdin) {
			return nil
		}
	}

	if terminal.IsTerminal(syscall.Stdin) {
		log.Info("Connecting to the instance...")
	}
	if err := connect.Connect(connectCtx, newArgs); err != nil {
		return err
	}

	return nil
}
